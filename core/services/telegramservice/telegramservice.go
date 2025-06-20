package telegramservice

import (
	"context"
	"fmt"

	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/core/taskengine"
	"github.com/contenox/contenox/core/tasksrecipes"
	"github.com/contenox/contenox/libs/libdb"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
)

type Service interface {
	HandleUpdate(ctx context.Context, updates ...tgbotapi.Update) error
	RunTick(ctx context.Context, offset int) (int, error)
	serverops.ServiceMeta
}

type service struct {
	bot        *tgbotapi.BotAPI
	env        taskengine.EnvExecutor
	dbInstance libdb.DBManager
}

func New(botToken string, env taskengine.EnvExecutor, dbInstance libdb.DBManager) (Service, error) {
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		return nil, err
	}
	return &service{bot: bot, env: env, dbInstance: dbInstance}, nil
}

// NewInstance creates a new chat instance after verifying that the user is authorized to start a chat for the given model.
func (s *service) newInstance(ctx context.Context, identity string, preferredModels ...string) (string, error) {
	tx := s.dbInstance.WithoutTransaction()

	idxID := uuid.New().String()
	err := store.New(tx).CreateMessageIndex(ctx, idxID, identity)
	if err != nil {
		return "", err
	}

	return idxID, nil
}

func (s *service) RunTick(ctx context.Context, offset int) (int, error) {
	u := tgbotapi.NewUpdate(offset)
	u.Timeout = 60

	updates, err := s.bot.GetUpdates(u)
	if err != nil {
		return offset, err
	}
	s.HandleUpdate(ctx, updates...)
	return offset + len(updates), nil
}

func (s *service) HandleUpdate(ctx context.Context, updates ...tgbotapi.Update) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for _, update := range updates {
		if update.Message.Command() == "new" {
			idxID, err := s.newInstance(ctx, fmt.Sprintf("%d", update.SentFrom().ID))
			if err != nil {
				return err
			}
			_, err = s.bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID, fmt.Sprintf("New instance created for %s: %s", update.SentFrom().UserName, idxID)))
			if err != nil {
				return err
			}
		}
		text := update.Message.Text
		subjID := fmt.Sprintf("%d", update.SentFrom().ID)

		chain := tasksrecipes.BuildChatChain(subjID)
		result, err := s.env.ExecEnv(ctx, chain, text, taskengine.DataTypeString)
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

		_, err = s.bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID, lastMsg.Content))
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *service) GetServiceName() string {
	return "telegramservice"
}

func (s *service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
