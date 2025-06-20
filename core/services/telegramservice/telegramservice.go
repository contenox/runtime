package telegramservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/core/taskengine"
	"github.com/contenox/contenox/core/tasksrecipes"
	"github.com/contenox/contenox/libs/libdb"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
)

var JobTypeTelegram string = "telegram"

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
}

func New(ctx context.Context, botToken string, workerUserAccountID string, env taskengine.EnvExecutor, dbInstance libdb.DBManager) (Worker, error) {
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		return nil, err
	}
	w := &worker{bot: bot, env: env, dbInstance: dbInstance}
	err = w.newInstance(ctx)
	if err != nil {
		return nil, err
	}
	w.workerUserAccountID = workerUserAccountID
	return w, nil
}

func (w *worker) newInstance(ctx context.Context) error {
	tx := w.dbInstance.WithoutTransaction()
	err := store.New(tx).CreateMessageIndex(ctx, w.workerUserAccountID, w.workerUserAccountID)
	if err != nil {
		return err
	}

	return nil
}

func (w *worker) ReceiveTick(ctx context.Context) error {
	tx, com, end, err := w.dbInstance.WithTransaction(ctx)
	defer end()
	if err != nil {
		return fmt.Errorf("start transaction: %w", err)
	}
	job, err := store.New(tx).PopJobForType(ctx, JobTypeTelegram)
	if err != nil && !errors.Is(err, libdb.ErrNotFound) {
		return fmt.Errorf("get last job: %w", err)
	}
	var offset int
	if !errors.Is(err, libdb.ErrNotFound) {
		var update tgbotapi.Update
		err = json.Unmarshal(job.Payload, &update)
		if err != nil {
			return fmt.Errorf("unmarshal job payload: %w", err)
		}
		message := update.Message
		if message != nil {
			offset = message.MessageID + 1 // as suggested by the telegram docs
		}
	}
	err = w.runTick(ctx, tx, offset)
	if err != nil {
		return err
	}
	if err := com(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return err
}

func (w *worker) runTick(ctx context.Context, tx libdb.Exec, offset int) error {
	u := tgbotapi.NewUpdate(offset)
	u.Timeout = 60
	u.Limit = 10

	updates, err := w.bot.GetUpdates(u)
	if err != nil {
		return err
	}

	if len(updates) == 0 {
		return nil
	}
	storeInstance := store.New(tx)
	jobs := make([]*store.Job, 0, len(updates))
	for _, update := range updates {
		id := fmt.Sprint(update.SentFrom().ID)
		_, err := storeInstance.GetUserBySubject(ctx, id)
		if err != nil && !errors.Is(err, libdb.ErrNotFound) {
			return err
		}
		if errors.Is(err, libdb.ErrNotFound) {
			err := storeInstance.CreateUser(ctx, &store.User{
				ID:           uuid.NewString(),
				FriendlyName: update.SentFrom().UserName,
				Subject:      id,
				Salt:         uuid.NewString(),
				Email:        id + "@telegramservice.contnox.com",
			})
			if err != nil {
				return err
			}
		}
		payload, err := json.Marshal(update)
		if err != nil {
			return err
		}
		jobs = append(jobs,
			&store.Job{
				ID:        uuid.NewString(),
				TaskType:  JobTypeTelegram,
				CreatedAt: time.Now().UTC(),
				Operation: "message",
				Payload:   payload,
			})
	}
	err = storeInstance.AppendJobs(ctx, jobs...)
	if err != nil {
		return fmt.Errorf("append message: %w", err)
	}

	return nil
}

func (w *worker) ProcessTick(ctx context.Context) error {
	storeInstance := store.New(w.dbInstance.WithoutTransaction())
	jobs, err := storeInstance.PopJobsForType(ctx, JobTypeTelegram)
	if err != nil {
		return fmt.Errorf("pop job: %w", err)
	}
	if jobs == nil {
		return nil
	}
	var errors []error
	updates := make([]*tgbotapi.Update, len(jobs))
	for i, job := range jobs {
		err = json.Unmarshal(job.Payload, &updates[i])
		if err != nil {
			errors = append(errors, fmt.Errorf("unmarshal update: %w", err))
			continue
		}
	}
	if len(errors) > 0 {
		return fmt.Errorf("process jobs: %s", errors)
	}
	for i, update := range updates {
		if jobs[i].RetryCount > 3 {
			err = fmt.Errorf("max retries reached")
			errors = append(errors, err)
			continue
		}
		err = w.Process(ctx, update)
		if err != nil {
			errors = append(errors, err)
		}
		failedJob := jobs[i]
		failedJob.RetryCount++
		storeInstance.AppendJob(ctx, *failedJob)
	}

	return nil
}

func (w *worker) Process(ctx context.Context, update *tgbotapi.Update) error {
	text := update.Message.Text
	subjID := fmt.Sprint(update.SentFrom().ID)

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
