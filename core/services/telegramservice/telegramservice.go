package telegramservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/core/taskengine"
	"github.com/contenox/contenox/core/tasksrecipes"
	"github.com/contenox/contenox/libs/libdb"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
)

var (
	JobTypeTelegram                string = "telegram"
	JobTypeTelegramWorkerOffsetKey string = "telegram-worker-offset-key"
	DefaultLeaseDuration                  = 30 * time.Second
)

type Worker interface {
	ReceiveTick(ctx context.Context) error
	ProcessTick(ctx context.Context) error
	Process(ctx context.Context, update *tgbotapi.Update) error
	serverops.ServiceMeta
}

type worker struct {
	bot                 *tgbotapi.BotAPI
	env                 taskengine.EnvExecutor
	dbInstance          libdb.DBManager
	workerUserAccountID string
	bootOffset          int
}

func New(ctx context.Context, botToken string, bootOffset int, env taskengine.EnvExecutor, dbInstance libdb.DBManager) (Worker, error) {
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		return nil, err
	}
	w := &worker{bot: bot, env: env, dbInstance: dbInstance, bootOffset: bootOffset}

	if w.dbInstance == nil {
		return nil, errors.New("db instance is nil")
	}
	var offset int
	storeInstance := store.New(w.dbInstance.WithoutTransaction())
	err = storeInstance.GetKV(ctx, JobTypeTelegramWorkerOffsetKey, &offset)
	if err != nil && err != libdb.ErrNotFound {
		return nil, err
	}
	if offset > bootOffset {
		w.bootOffset = offset
	}

	return w, nil
}

func (w *worker) ReceiveTick(ctx context.Context) error {
	tx, com, end, err := w.dbInstance.WithTransaction(ctx)
	defer end()
	if err != nil {
		return fmt.Errorf("start transaction: %w", err)
	}

	err = w.runTick(ctx, tx)
	if err != nil {
		return err
	}
	if err := com(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (w *worker) runTick(ctx context.Context, tx libdb.Exec) error {
	var offset int
	storeInstance := store.New(tx)
	err := storeInstance.GetKV(ctx, JobTypeTelegramWorkerOffsetKey, &offset)
	if err != nil && !errors.Is(err, libdb.ErrNotFound) {
		return fmt.Errorf("get offset: %w", err)
	}
	if offset < w.bootOffset {
		offset = w.bootOffset
	}

	u := tgbotapi.NewUpdate(offset)
	u.Timeout = 60
	u.Limit = 5

	updates, err := w.bot.GetUpdates(u)
	if err != nil {
		return err
	}

	if len(updates) == 0 {
		return nil
	}

	jobs := make([]*store.Job, 0, len(updates))
	for _, update := range updates {
		// Skip if this is not a message update
		if update.Message == nil {
			continue
		}

		userID := fmt.Sprint(update.SentFrom().ID)
		subjID := fmt.Sprint(update.FromChat().ID) + userID

		// Check/create user
		_, err := storeInstance.GetUserBySubject(ctx, userID)
		if err != nil && !errors.Is(err, libdb.ErrNotFound) {
			return err
		}
		if errors.Is(err, libdb.ErrNotFound) {
			err := storeInstance.CreateUser(ctx, &store.User{
				ID:           userID,
				FriendlyName: update.SentFrom().UserName,
				Subject:      userID,
				Salt:         uuid.NewString(),
				Email:        userID + "@telegramservice.contnox.com",
			})
			if err != nil {
				return err
			}
		}

		// Check/create message index
		found := false
		idxs, err := storeInstance.ListMessageIndices(ctx, userID)
		if err != nil && !errors.Is(err, libdb.ErrNotFound) {
			return err
		}
		if slices.Contains(idxs, subjID) {
			found = true
		}
		if !found {
			err := storeInstance.CreateMessageIndex(ctx, subjID, userID)
			if err != nil {
				return err
			}
		}

		// Create job
		payload, err := json.Marshal(update)
		if err != nil {
			return err
		}

		jobs = append(jobs, &store.Job{
			ID:        uuid.NewString(),
			TaskType:  JobTypeTelegram,
			CreatedAt: time.Now().UTC(),
			Operation: "message",
			Payload:   payload,
			Subject:   subjID,
		})
	}

	// Append all jobs at once
	if err := storeInstance.AppendJobs(ctx, jobs...); err != nil {
		return fmt.Errorf("append message: %w", err)
	}

	// Update offset to the last processed update
	if len(updates) > 0 {
		lastUpdate := updates[len(updates)-1]
		offs, err := json.Marshal(lastUpdate.UpdateID + 1)
		if err != nil {
			return err
		}
		if err := storeInstance.SetKV(ctx, JobTypeTelegramWorkerOffsetKey, offs); err != nil {
			return err
		}
	}

	return nil
}

func (w *worker) ProcessTick(ctx context.Context) error {
	storeInstance := store.New(w.dbInstance.WithoutTransaction())
	leaseID := uuid.NewString()
	// Try to lease a job
	leasedJob, err := storeInstance.PopJobForType(ctx, JobTypeTelegram)
	if err != nil {
		if errors.Is(err, libdb.ErrNotFound) {
			return nil // No jobs available
		}
		return fmt.Errorf("pop job: %w", err)
	}

	// Lease the job
	leaseDuration := DefaultLeaseDuration
	err = storeInstance.AppendLeasedJob(ctx, *leasedJob, leaseDuration, leaseID)
	if err != nil {
		return fmt.Errorf("lease job: %w", err)
	}

	// Process the leased job
	var update tgbotapi.Update
	if err := json.Unmarshal(leasedJob.Payload, &update); err != nil {
		// If unmarshal fails, mark as failed (won't retry)
		_ = storeInstance.DeleteLeasedJob(ctx, leasedJob.ID)
		return fmt.Errorf("unmarshal update: %w", err)
	}

	processErr := w.Process(ctx, &update)

	// Mark job as completed or failed
	if processErr == nil {
		// Success - delete the leased job
		if err := storeInstance.DeleteLeasedJob(ctx, leasedJob.ID); err != nil {
			return fmt.Errorf("delete leased job: %w", err)
		}
	} else {
		// Failure - handle retry logic
		if leasedJob.RetryCount >= 30 {
			// Max retries reached - delete the job
			_ = storeInstance.DeleteLeasedJob(ctx, leasedJob.ID)
			return fmt.Errorf("job %s: max retries reached", leasedJob.ID)
		}

		// Requeue with backoff
		leasedJob.RetryCount++
		// backoff := time.Duration(leasedJob.RetryCount*leasedJob.RetryCount) * time.Second
		// leasedJob.ScheduledFor = time.Now().Add(backoff).Unix()

		// Delete the leased job and requeue
		if err := storeInstance.DeleteLeasedJob(ctx, leasedJob.ID); err != nil {
			return fmt.Errorf("delete leased job for requeue: %w", err)
		}
		if err := storeInstance.AppendJob(ctx, *leasedJob); err != nil {
			return fmt.Errorf("requeue job: %w", err)
		}

		return fmt.Errorf("process job failed: %w", processErr)
	}

	return nil
}

func (w *worker) Process(ctx context.Context, update *tgbotapi.Update) error {
	text := update.Message.Text
	subjID := fmt.Sprint(update.FromChat().ID) + fmt.Sprint(update.SentFrom().ID)

	chain := tasksrecipes.BuildChatChain(subjID)
	result, err := w.env.ExecEnv(ctx, chain, text, taskengine.DataTypeString)
	if err != nil {
		return fmt.Errorf("chain execution failed: %w", err)
	}
	hist, ok := result.(taskengine.ChatHistory)
	if !ok || len(hist.Messages) == 0 {
		return fmt.Errorf("invalid chat history")
	}
	lastMsg := hist.Messages[len(hist.Messages)-1]
	if lastMsg.Role != "assistant" && lastMsg.Role != "system" {
		return fmt.Errorf("invalid chat history 2")
	}

	_, err = w.bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID, lastMsg.Content))
	if err != nil {
		return err
	}

	return nil
}

func (w *worker) GetServiceName() string {
	return "telegramservice"
}

func (w *worker) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
