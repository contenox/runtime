package telegramservice

import (
	"context"

	"github.com/contenox/contenox/core/services/chatservice"
	"github.com/contenox/contenox/core/services/tokenizerservice"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Service struct {
	bot       *tgbotapi.BotAPI
	chatSvc   *chatservice.Service
	tokenizer tokenizerservice.Tokenizer
}

func New(botToken string, chatSvc *chatservice.Service, tokenizer tokenizerservice.Tokenizer) (*Service, error) {
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		return nil, err
	}
	return &Service{bot: bot, chatSvc: chatSvc, tokenizer: tokenizer}, nil
}

func (s *Service) Run(ctx context.Context) error {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := s.bot.GetUpdatesChan(u)

	for {
		select {
		case update := <-updates:
			if update.Message == nil {
				continue
			}
			s.handleMessage(update.Message)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *Service) handleMessage(msg *tgbotapi.Message) {
	// Handle commands like /echo, /search, etc.
	switch msg.Command() {
	case "start":
		s.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Welcome!"))
	case "chat":
		// reply, _, _, err := s.chatSvc.Chat(context.Background(), msg.FromUserName, msg.Text, "default-model")
		// if err != nil {
		// 	s.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Error: "+err.Error()))
		// } else {
		// 	s.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, reply))
		// }
	default:
		s.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Unknown command"))
	}
}
