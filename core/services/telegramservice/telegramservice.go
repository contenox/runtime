package telegramservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/core/taskengine"
	"github.com/contenox/runtime-mvp/core/tasksrecipes"
	"github.com/contenox/runtime-mvp/libs/libdb"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
)

var (
	JobTypeTelegram                string = "telegram"
	JobTypeTelegramWorkerOffsetKey string = "telegram-worker-offset-key"
	DefaultLeaseDuration                  = 30 * time.Second
	provider                       string = "gemini"
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
		if update.Message == nil {
			continue
		}

		job, err := w.createJobForUpdate(ctx, storeInstance, update)
		if err != nil {
			return fmt.Errorf("create job: %w", err)
		}
		jobs = append(jobs, job)
	}

	if err := storeInstance.AppendJobs(ctx, jobs...); err != nil {
		return fmt.Errorf("append jobs: %w", err)
	}

	return w.updateOffset(ctx, storeInstance, updates)
}

func (w *worker) createJobForUpdate(ctx context.Context, storeInstance store.Store, update tgbotapi.Update) (*store.Job, error) {
	userID := fmt.Sprint(update.SentFrom().ID)
	subjID := fmt.Sprint(update.FromChat().ID) + userID

	if err := w.ensureUserExists(ctx, storeInstance, update, userID); err != nil {
		return nil, err
	}

	if err := w.ensureMessageIndexExists(ctx, storeInstance, userID, subjID); err != nil {
		return nil, err
	}

	payload, err := json.Marshal(update)
	if err != nil {
		return nil, err
	}

	return &store.Job{
		ID:        uuid.NewString(),
		TaskType:  JobTypeTelegram,
		CreatedAt: time.Now().UTC(),
		Operation: "message",
		Payload:   payload,
		Subject:   subjID,
	}, nil
}

func (w *worker) ensureUserExists(ctx context.Context, storeInstance store.Store, update tgbotapi.Update, userID string) error {
	_, err := storeInstance.GetUserBySubject(ctx, userID)
	if err == nil {
		return nil
	}

	if !errors.Is(err, libdb.ErrNotFound) {
		return err
	}

	return storeInstance.CreateUser(ctx, &store.User{
		ID:           userID,
		FriendlyName: update.SentFrom().UserName,
		Subject:      userID,
		Salt:         uuid.NewString(),
		Email:        userID + "@telegramservice.contnox.com",
	})
}

func (w *worker) ensureMessageIndexExists(ctx context.Context, storeInstance store.Store, userID, subjID string) error {
	idxs, err := storeInstance.ListMessageIndices(ctx, userID)
	if err != nil && !errors.Is(err, libdb.ErrNotFound) {
		return err
	}

	if slices.Contains(idxs, subjID) {
		return nil
	}

	return storeInstance.CreateMessageIndex(ctx, subjID, userID)
}

func (w *worker) updateOffset(ctx context.Context, storeInstance store.Store, updates []tgbotapi.Update) error {
	if len(updates) == 0 {
		return nil
	}

	lastUpdate := updates[len(updates)-1]
	offset := lastUpdate.UpdateID + 1

	offs, err := json.Marshal(offset)
	if err != nil {
		return err
	}
	return storeInstance.SetKV(ctx, JobTypeTelegramWorkerOffsetKey, offs)
}

func (w *worker) ProcessTick(ctx context.Context) error {
	storeInstance := store.New(w.dbInstance.WithoutTransaction())
	leaseID := uuid.NewString()

	leasedJob, err := storeInstance.PopJobForType(ctx, JobTypeTelegram)
	if err != nil {
		if errors.Is(err, libdb.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("pop job: %w", err)
	}

	return w.processLeasedJob(ctx, storeInstance, leasedJob, leaseID)
}

func (w *worker) processLeasedJob(ctx context.Context, storeInstance store.Store, leasedJob *store.Job, leaseID string) error {
	leaseDuration := DefaultLeaseDuration
	if err := storeInstance.AppendLeasedJob(ctx, *leasedJob, leaseDuration, leaseID); err != nil {
		return fmt.Errorf("lease job: %w", err)
	}

	var update tgbotapi.Update
	if err := json.Unmarshal(leasedJob.Payload, &update); err != nil {
		_ = storeInstance.DeleteLeasedJob(ctx, leasedJob.ID)
		return fmt.Errorf("unmarshal update: %w", err)
	}

	processErr := w.Process(ctx, &update)

	if processErr == nil {
		return storeInstance.DeleteLeasedJob(ctx, leasedJob.ID)
	}

	if leasedJob.RetryCount >= 30 {
		_ = storeInstance.DeleteLeasedJob(ctx, leasedJob.ID)
		return fmt.Errorf("job %s: max retries reached", leasedJob.ID)
	}

	leasedJob.RetryCount++
	if err := storeInstance.DeleteLeasedJob(ctx, leasedJob.ID); err != nil {
		return fmt.Errorf("delete leased job for requeue: %w", err)
	}

	if err := storeInstance.AppendJob(ctx, *leasedJob); err != nil {
		return fmt.Errorf("requeue job: %w", err)
	}

	return fmt.Errorf("process job failed: %w", processErr)
}

func (w *worker) Process(ctx context.Context, update *tgbotapi.Update) error {
	text := update.Message.Text
	subjID := fmt.Sprint(update.FromChat().ID) + fmt.Sprint(update.SentFrom().ID)

	chain := tasksrecipes.BuildChatChain(tasksrecipes.BuildChatChainReq{
		SubjectID: subjID,
		Provider:  provider,
	})
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
	return err
}

func (w *worker) GetServiceName() string {
	return "telegramservice"
}

func (w *worker) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
