package scheduler

import (
	"errors"
	"fmt"
	"time"

	"github.com/go-pg/pg"
	"github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/jqs7/zwei/model"
)

type Scheduler struct {
	*pg.DB
	*tgbotapi.BotAPI
}

func New(db *pg.DB, bot *tgbotapi.BotAPI) *Scheduler {
	db.Model(&model.Task{}).
		Where("status = ?", model.TaskStatusDoing).
		Set("status = ?", model.TaskStatusPlan).
		Update()
	return &Scheduler{
		DB:     db,
		BotAPI: bot,
	}
}

func (s Scheduler) Run() error {
	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()
	for range ticker.C {
		var tasks []model.Task
		err := s.Model(&model.Task{}).
			Where("status = ?", model.TaskStatusPlan).
			Where("run_at <= ?", time.Now()).
			Limit(10).
			Select(&tasks)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			s.processTask(task)
		}
	}
	return nil
}

func (s Scheduler) processTask(task model.Task) error {
	_, err := s.Model(&task).WherePK().
		Set("status = ?", model.TaskStatusDoing).
		Update()
	if err != nil {
		return err
	}
	switch task.Type {
	case model.TaskTypeDeleteMsg:
		s.DeleteMessage(tgbotapi.NewDeleteMessage(task.ChatID, task.MsgID))
		_, err = s.Model(&task).WherePK().
			Set("status = ?", model.TaskStatusDone).
			Update()
		return err
	case model.TaskTypeUpdateMsgExpire:
		if err := s.updateMsgExpire(task); err != nil {
			_, err = s.Model(&task).WherePK().
				Set("run_at = ?", time.Now().Add(time.Second*3)).
				Set("status = ?", model.TaskStatusPlan).
				Update()
			return err
		}
		_, err = s.Model(&task).WherePK().
			Set("status = ?", model.TaskStatusDone).
			Update()
		return err
	}
	return nil
}

func (s Scheduler) updateMsgExpire(task model.Task) error {
	blackList := &model.BlackList{Id: task.BlackListId}
	s.Model(blackList).
		WherePK().First()
	timeSub := blackList.ExpireAt.Sub(time.Now()) / time.Second
	if timeSub <= 0 {
		return s.delAndKick(blackList)
	}
	s.updateMsg(blackList, timeSub)
	return errors.New("not expired")
}

func (s Scheduler) delAndKick(blackList *model.BlackList) error {
	s.Send(tgbotapi.NewDeleteMessage(
		blackList.GroupId, blackList.CaptchaMsgId,
	))
	s.KickChatMember(tgbotapi.KickChatMemberConfig{
		ChatMemberConfig: tgbotapi.ChatMemberConfig{
			ChatID: blackList.GroupId,
			UserID: blackList.UserId,
		},
		UntilDate: time.Now().Add(time.Minute).Unix(),
	})
	return nil
}

func (s Scheduler) updateMsg(blackList *model.BlackList, timeSub time.Duration) error {
	chat, err := s.GetChat(tgbotapi.ChatConfig{ChatID: blackList.GroupId})
	if err != nil {
		return err
	}
	caption := fmt.Sprintf(model.EnterRoomMsg, blackList.UserLink, chat.Title, timeSub)
	editor := tgbotapi.NewEditMessageCaption(blackList.GroupId, blackList.CaptchaMsgId, caption)
	editor.ReplyMarkup = &model.InlineKeyboard
	editor.ParseMode = tgbotapi.ModeMarkdown
	_, err = s.Send(editor)
	return err
}

func AddDelMsgTask(db *pg.DB, chatID int64, msgID int) error {
	return db.Insert(&model.Task{
		Type:   model.TaskTypeDeleteMsg,
		Status: model.TaskStatusPlan,
		RunAt:  time.Now().Add(time.Second * 10),
		ChatID: chatID,
		MsgID:  msgID,
	})
}

func AddUpdateMsgExpireTask(db *pg.DB, blackListID, chatID int64, msgID int) error {
	return db.Insert(&model.Task{
		Type:        model.TaskTypeUpdateMsgExpire,
		Status:      model.TaskStatusPlan,
		RunAt:       time.Now().Add(time.Second * 3),
		ChatID:      chatID,
		MsgID:       msgID,
		BlackListId: blackListID,
	})
}

func UpdateMsgExpireTaskDone(db *pg.DB, blackListID int64) error {
	_, err := db.Model(&model.Task{}).
		Where("type = ?", model.TaskTypeUpdateMsgExpire).
		Where("black_list_id = ?", blackListID).
		Set("status = ?", model.TaskStatusDone).
		Update()
	return err
}
