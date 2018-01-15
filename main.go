package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	golosClient "github.com/asuleymanov/golos-go/client"
	"github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/grokify/html-strip-tags-go"

	configuration "github.com/GolosTools/golos-vote-bot/config"
	"github.com/GolosTools/golos-vote-bot/db"
	"github.com/GolosTools/golos-vote-bot/helpers"
	"github.com/GolosTools/golos-vote-bot/models"
)

const (
	buttonAddKey        = "🐬Делегировать"
	buttonRemoveKey     = "🦀Остановить"
	buttonSetPowerLimit = "💪Настройка"
	buttonInformation   = "⚓️Информация"
	buttonWannaCurate   = "Стать куратором"
	buttonStopCurate    = "Прекратить кураторство"
)

var (
	config   configuration.Config
	database *sql.DB
	bot      *tgbotapi.BotAPI
)

func main() {
	err := configuration.LoadConfiguration("./config.json", &config)
	if err != nil {
		log.Panic(err)
	}
	err = configuration.LoadConfiguration("./config.local.json", &config)
	if err != nil && !os.IsNotExist(err) {
		log.Panic(err)
	}
	if config.TelegramToken == "write-your-telegram-token-here" {
		log.Panic("Токен для телеграма не введён")
	}

	golosClient.Key_List[config.Account] = golosClient.Keys{
		PKey: config.PostingKey,
		AKey: config.ActiveKey}

	database, err = db.InitDB(config.DatabasePath)
	if err != nil {
		if err.Error() == "unable to open database file" {
			path, err := filepath.Abs(config.DatabasePath)
			if err != nil {
				log.Panic(err)
			}
			log.Panic(fmt.Sprintf("unable to open database at path: %s", path))
		}
		log.Panic(err)
	}
	defer database.Close()

	bot, err = tgbotapi.NewBotAPI(config.TelegramToken)
	if err != nil {
		log.Panic(err)
	}
	bot.Debug = false
	log.Printf("Authorized on account %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	
	go checkAuthority()

	updates, err := bot.GetUpdatesChan(u)
	if err != nil {
		log.Panic(err)
	}
	for update := range updates {
		err := processMessage(update)
		if err != nil {
			log.Println(err)
		}
	}
}

func processMessage(update tgbotapi.Update) error {
	chatID, err := helpers.GetChatID(update)
	if err != nil {
		return err
	}
	userID, err := helpers.GetUserID(update)
	if err != nil {
		return err
	}
	msg := tgbotapi.NewMessage(chatID, "")

	state, err := models.GetStateByUserID(userID, database)
	if err != nil {
		return err
	}

	if update.Message != nil {
		domainRegexp, err := helpers.GetDomainRegexp(config.Domains)
		if err != nil {
			return err
		}
		if update.Message.Chat.Type != "private" {
			return nil
		}
		switch {
		case update.Message.IsCommand():
			switch update.Message.Command() {
			case "start":
				username := "%username%"
				if len(update.Message.From.FirstName) > 0 {
					username = update.Message.From.FirstName
				}
				msg.Text = fmt.Sprintf("Привет, %s! \n\n"+
					"Я — бот для коллективного кураторства в [социальной блокчейн-сети \"Голос\"](https://golos.io).\n\n"+
					"Мой код полностью открыт и находится здесь: %s\n\n"+
					"Предлагаю начать с нажатия кнопки \""+buttonAddKey+"\", "+
					"после чего я дам ссылку на группу для предложения постов.\n\n"+
					"По любым вопросам пиши моему хозяину — %s",
					username, config.Repository, config.Developer)
				// save referral if exists
				if len(update.Message.CommandArguments()) > 0 {
					_, err := models.GetCredentialByUserID(userID, database)
					if err == sql.ErrNoRows {
						decodedString, err := base64.URLEncoding.DecodeString(update.Message.CommandArguments())
						if err == nil {
							// TODO: проверить существование этого юзера
							referrer := string(decodedString)
							if !models.IsReferrerExists(referrer, database) {
								referral := models.Referral{UserID: userID, Referrer: referrer, Completed: false}
								_, err = referral.Save(database)
								if err != nil {
									log.Println("не сохранили реферала: " + err.Error())
								}
							} else {
								log.Println("Реферал уже привлекался ранее")
							}
						} else {
							log.Printf("не смогли раскодировать строку %s", update.Message.CommandArguments())
						}
					}
				}
			case "newtest":
				if userID == config.Tester {
					if len(update.Message.CommandArguments()) > 0 {
						newID, _ := strconv.Atoi(update.Message.CommandArguments())
						oldID := userID
						_, err := models.GetCredentialByUserID(newID, database)
						if newID < 0 && err == sql.ErrNoRows {
							models.REFchangeUserID(database, oldID, newID)
							models.CREDchangeUserID(database, oldID, newID)
							msg.Text = "Done "
						}
					}
				}
			case "switch":
				if userID == config.Tester {
					if len(update.Message.CommandArguments()) > 0 {
						newID, _ := strconv.Atoi(update.Message.CommandArguments())
						oldID := userID
						_, err := models.GetCredentialByUserID(newID, database)
						if newID < 0 && err != sql.ErrNoRows {
							models.REFchangeUserID(database, oldID, 0)
							models.REFchangeUserID(database, newID, oldID)
							models.REFchangeUserID(database, 0, newID)
							models.CREDchangeUserID(database, oldID, 0)
							models.CREDchangeUserID(database, newID, oldID)
							models.CREDchangeUserID(database, 0, newID)
							msg.Text = "Done"
						}
					}
				}
			case "info":
				if userID == config.Tester {
					msg.Text, _ = models.GetTestCredentials(database)
				}
			}
			state.Action = update.Message.Command()
		case update.Message.Text == buttonAddKey:
			msg.Text = fmt.Sprintf("Добавь доверенный аккаунт *%s* в https://golostools.github.io/golos-vote-bot/ "+
				"(или через [форму от vik'a](https://golos.cf/multi/)), "+
				"а затем скажи мне свой логин на Голосе", config.Account)
			state.Action = buttonAddKey
		case update.Message.Text == buttonRemoveKey:
			msg.Text = fmt.Sprintf("Произошла ошибка, свяжись с разработчиком - %s", config.Developer)
			isActive := models.IsActiveCredential(userID, database)
			if isActive {
				credential, err := models.GetCredentialByUserID(userID, database)
				credential.Active = false
				result, err := credential.Save(database)
				if true == result && err == nil {
					msg.Text = "Отлично, я больше не буду использовать твой аккаунт при курировании постов. " +
						"Дополнительно можешь удалить все сторонние ключи из своего аккаунта здесь: " +
						"https://golos.cf/multi/off.html"
				}
			} else {
				msg.Text = "Аккаунт не активирован"
			}
			state.Action = buttonRemoveKey
		case update.Message.Text == buttonSetPowerLimit:
			if false == models.IsActiveCredential(userID, database) {
				msg.Text = "Сначала делегируй мне права кнопкой " + buttonAddKey
				break
			}
			msg.Text = "Введи значение делегируемой силы Голоса от 1 до 100%"
			state.Action = buttonSetPowerLimit
		case update.Message.Text == buttonInformation:
			if false == models.IsActiveCredential(userID, database) {
				msg.Text = "У меня пока нет информации для тебя"
				break
			}
			credential, err := models.GetCredentialByUserID(userID, database)
			if err != nil {
				return err
			}
			encodedUserName := base64.URLEncoding.EncodeToString([]byte(credential.UserName))
			referralLink := "https://t.me/" + config.TelegramBotName + "?start=" + encodedUserName
			msg.Text = fmt.Sprintf("Аккаунт: *%s*\n"+
				"Делегированная сила: *%d%%*\n"+
				"Ссылка для приглашения: [%s](%s)\n(в случае успеха дает обоим по %.3f Силы Голоса)",
				credential.UserName, credential.Power, referralLink, referralLink, config.ReferralFee)
			state.Action = buttonInformation
		case update.Message.Text == buttonWannaCurate:
			if models.IsCuratorExists(userID, database) {
				if models.IsCuratorActive(userID, database) {
					msg.Text = "Ты уже являешься куратором"
				} else {
					state.Action = buttonWannaCurate
					msg.Text = "Правила курирования"
				}
			} else {
				_, err = models.NewCurator(userID, chatID, database)
				if err != nil {
					return nil
				}
				state.Action = buttonWannaCurate
				msg.Text = "Правила курирования"
			}
		case update.Message.Text == buttonStopCurate:
			if models.IsCuratorExists(userID, database) {
				_, err = models.DeactivateCurator(userID, database)
				if err != nil {
					return nil
				}
				msg.Text = "Бремя кураторства покинуло тебя. Когда вдоволь насладишься свободой - возвращайся"
				state.Action = "deactivatedCurator"
			} else {
				msg.Text = "То, что мертво - умереть не может. Так и ты - нельзя отказаться от курирования, не будучи куратором"
			}
		case domainRegexp.MatchString(update.Message.Text):
			msg.ReplyToMessageID = update.Message.MessageID

			matched := domainRegexp.FindStringSubmatch(update.Message.Text)
			author, permalink := matched[1], matched[2]

			golos := golosClient.NewApi(config.Rpc, config.Chain)
			defer golos.Rpc.Close()
			post, err := golos.Rpc.Database.GetContent(author, permalink)
			if err != nil {
				return err
			}
			// check post exists in blockchain
			if post.Author != author || post.Permlink != permalink {
				return nil
			}

			if models.GetTodayVotesCountForUserID(userID, database) >= config.MaximumUserVotesPerDay {
				msg.Text = "Лимит твоих постов на сегодня превышен. Приходи завтра!"
				break
			}

			if models.GetLastVote(database).UserID == userID {
				msg.Text = "Нельзя предлагать два поста подряд. Наберись терпения!"
				break
			}

			if config.Censorship {
				tags := post.JsonMetadata.Tags
				includesBannedTag := false
				for _, bannedTag := range config.BannedTags {
					for _, postTag := range tags {
						if postTag == bannedTag {
							includesBannedTag = true
							msg.Text = "Нельзя предлагать посты с тегом " + postTag
						}
					}

				}
				if includesBannedTag {
					break
				}
			}

			isActive := models.IsActiveCredential(userID, database)
			if isActive == false {
				msg.Text = "Предлагать посты для голосования могут только голосующие пользователи. Жулик не воруй!"
				break
			}

			if post.Mode != "first_payout" {
				msg.Text = "Выплата за пост уже была произведена! Есть что-нибудь посвежее?"
				break
			}

			if post.MaxAcceptedPayout == "0.000 GBG" {
				msg.Text = "Мне не интересно голосовать за пост с отключенными выплатами"
				break
			}

			if helpers.IsVoxPopuli(author) && config.IgnoreVP {
				msg.Text = "Сообщества vox-populi могут сами себя поддержать"
				break
			}

			if len(post.Body) < config.MinimumPostLength {
				msg.Text = "Слишком мало текста, не скупись на буквы!"
				break
			}

			percent := 100

			voteModel := models.Vote{
				UserID:    userID,
				Author:    author,
				Permalink: permalink,
				Percent:   percent,
				Date:      time.Now(),
			}

			if voteModel.Exists(database) {
				msg.Text = "Уже голосовала за этот пост!"
				break
			}

			voteID, err := voteModel.Save(database)
			if err != nil {
				return err
			}

			log.Printf("Вкинули статью \"%s\" автора \"%s\" в чате %d", permalink, author, chatID)

			if checkUniqueness(voteModel) {
				go newPost(voteID, author, permalink, chatID)
			}
			
			return nil
		case state.Action == buttonAddKey:
			login := strings.ToLower(update.Message.Text)
			login = strings.Trim(login, "@")
			credential := models.Credential{
				UserID:   userID,
				UserName: login,
				Power:    100,
				Active:   true,
			}

			golos := golosClient.NewApi(config.Rpc, config.Chain)
			defer golos.Rpc.Close()
			accounts, err := golos.Rpc.Database.GetAccounts([]string{login})
			if err != nil {
				return err
			} else if len(accounts) == 1 {
				hasPostingAuth := false
				for _, auth := range accounts[0].Posting.AccountAuths {
					if auth.([]interface{})[0] == config.Account {
						hasPostingAuth = true
						break
					}
				}
				if hasPostingAuth {
					// send referral fee
					referral, err := models.GetReferralByUserID(userID, database)
					if err == nil && false == referral.Completed {
						if err = referral.SetCompleted(database); err == nil {
							_, err = models.GetCredentialByUserName(credential.UserName, database)
							if err == sql.ErrNoRows {
								go sendReferralFee(referral.Referrer, credential.UserName)
							}
						}
					}

					_, err = credential.Save(database)
					if err != nil {
						return err
					}
					msg.Text = "Поздравляю, теперь ты полноправный куратор! " +
						"Присоединяйся к нашей группе для участия в курировании: " + config.GroupLink
					state.Action = "successAuth"
				} else {
					msg.Text = fmt.Sprintf("Доступ у этого аккаунта для меня отсутствует. "+
						"Добавить его можно в https://golos.cf/multi/ для аккаунта *%s*", config.Account)
				}
			} else {
				msg.Text = fmt.Sprintf("Что-то пошло не так. Попробуй повторить позже "+
					"или свяжись с разработчиком: %s", config.Developer)
				log.Printf("Введён некорректный логин: %s", update.Message.Text)
			}
		case state.Action == buttonSetPowerLimit:
			re := regexp.MustCompile("[0-9]+")
			valueString := re.FindString(update.Message.Text)
			value, err := strconv.Atoi(valueString)
			if err != nil {
				msg.Text = "Не поняла. Введи значение делегируемой силы Голоса от 1 до 100%"
				break
			}
			if value >= 1 && value <= 100 {
				if false == models.IsActiveCredential(userID, database) {
					msg.Text = "Сначала делегируй мне права кнопкой " + buttonAddKey
					break
				}

				credential, err := models.GetCredentialByUserID(userID, database)
				if err != nil {
					return err
				}

				golos := golosClient.NewApi(config.Rpc, config.Chain)
				defer golos.Rpc.Close()

				accounts, err := golos.Rpc.Database.GetAccounts([]string{credential.UserName})
				if err != nil {
					return err
				}

				voteWeightThreshold := 1.0 * 1000.0 * 1000.0
				vestingSharesPreparedString := strings.Split(accounts[0].VestingShares, " ")[0]
				vestingShares, err := strconv.ParseFloat(vestingSharesPreparedString, 64)
				if err != nil {
					return err
				}
				if vestingShares > voteWeightThreshold {
					err = credential.UpdatePower(value, database)
					if err != nil {
						return err
					}
					msg.Text = fmt.Sprintf("Предоставленная мне в распоряжение сила Голоса "+
						"для аккаунта *%s* теперь равна *%d%%*", credential.UserName, value)
				} else {
					msg.Text = "У тебя пока слишком маленькая Сила Голоса для этого"
				}
				state.Action = "updatedPower"
			}
		case state.Action == buttonWannaCurate:
			if update.Message.Text == "Я все понял, все еще хочу курировать" {
				if models.ActivateCurator(UserId, database) {
					return nil
				}
				msg.Text = "Отлично, теперь ты можешь участвовать в курировании постов"
				state.Action = "activatedCurator"
			}
		default:
			if update.Message.Chat.Type != "private" {
				return nil
			}
			msg.Text = "Не понимаю"
		}
		if msg.ReplyMarkup == nil && update.Message.Chat.Type == "private" {
			firstButton := tgbotapi.NewKeyboardButton(buttonAddKey)
			secondButton := tgbotapi.NewKeyboardButton(buttonRemoveKey)
			firstButtonRow := []tgbotapi.KeyboardButton{firstButton, secondButton}
			
			thirdButton := tgbotapi.NewKeyboardButton(buttonSetPowerLimit)
			fourthButton := tgbotapi.NewKeyboardButton(buttonInformation)
			secondButtonRow := []tgbotapi.KeyboardButton{thirdButton, fourthButton}
			
			fifthButton := tgbotapi.NewKeyboardButton(buttonWannaCurate)
			sixthButton := tgbotapi.NewKeyboardButton(buttonStopCurate)
			thirdButtonRow := []tgbotapi.KeyboardButton{fifthButton, sixthButton}
			
			keyboard := tgbotapi.NewReplyKeyboard(firstButtonRow, secondButtonRow, thirdButtonRow)
			msg.ReplyMarkup = keyboard
		}
	} else if update.CallbackQuery != nil {
		arr := strings.Split(update.CallbackQuery.Data, "_")
		voteStringID, action := arr[0], arr[1]
		voteID, err := strconv.ParseInt(voteStringID, 10, 64)
		if err != nil {
			return err
		}

		if models.IsActiveCurator(userID, database) == false {
			config := tgbotapi.CallbackConfig{
				CallbackQueryID: update.CallbackQuery.ID,
				Text:            "Чекни свои привелегии. Ты не куратор!",
			}
			bot.AnswerCallbackQuery(config)
			return nil
		}

		voteModel := models.GetVote(database, voteID)
		if voteModel.Completed {
			return nil
		}
		if voteModel.UserID == userID {
			config := tgbotapi.CallbackConfig{
				CallbackQueryID: update.CallbackQuery.ID,
				Text:            "Твоя власть не безгранична, Куратор. Нельзя голосовать за свой же пост!",
			}
			bot.AnswerCallbackQuery(config)
			return nil
		}

		isGood := action == "good"
		response := models.Response{
			UserID: userID,
			VoteID: voteID,
			Result: isGood,
			Date:   time.Now(),
		}
		text := "И да настигнет Админская кара всех тех, кто пытается злоупотреблять своей властью и голосовать несколько раз! Админь"
		responseExists := response.Exists(database)
		if !responseExists {
			text = "Голос принят, Куратор. Продолжай в том же духе"
		}

		callbackConfig := tgbotapi.CallbackConfig{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            text,
		}
		bot.AnswerCallbackQuery(callbackConfig)

		if !responseExists {
			_, err := response.Save(database)
			if err != nil {
				return err
			}
		}
		return nil
	}

	_, err = state.Save(database)
	if err != nil {
		return err
	}

	if msg.Text == "" {
		return errors.New("отсутствует текст сообщения")
	}

	msg.ParseMode = "Markdown"
	msg.DisableWebPagePreview = true
	_, err = bot.Send(msg)
	if err != nil {
		return err
	}
	return nil
}

func verifyVotes(voteModel models.Vote, update tgbotapi.Update) error {
	chatID, err := helpers.GetChatID(update)
	if err != nil {
		return err
	}
	messageID, err := helpers.GetMessageID(update)
	if err != nil {
		return err
	}

	responses, err := models.GetAllResponsesForVoteID(voteModel.VoteID, database)
	if err != nil {
		return err
	}

	var positives, negatives int
	for _, response := range responses {
		if response.Result {
			positives = positives + 1
		} else {
			negatives = negatives + 1
		}
	}

	markup := helpers.GetVoteMarkup(voteModel.VoteID, positives, negatives)
	updateTextConfig := tgbotapi.EditMessageTextConfig{
		BaseEdit: tgbotapi.BaseEdit{
			ChatID:      chatID,
			MessageID:   messageID,
			ReplyMarkup: &markup,
		},
		Text: update.CallbackQuery.Message.Text,
	}
	bot.Send(updateTextConfig)

	if positives+negatives >= config.RequiredVotes {
		if voteModel.Completed {
			return nil
		}
		voteModel.Completed = true
		_, err := voteModel.Save(database)
		if err != nil {
			return err
		}
		msg := tgbotapi.NewEditMessageText(chatID, messageID, "")
		if positives >= negatives {
			go vote(voteModel, chatID, messageID)
			return nil
		} else {
			msg.Text = "Пост отклонен"
		}
		_, err = bot.Send(msg)
		if err != nil {
			return err
		}
	}
	return nil
}

func removeUser(bot *tgbotapi.BotAPI, chatID int64, userID int) error {
	memberConfig := tgbotapi.KickChatMemberConfig{
		ChatMemberConfig: tgbotapi.ChatMemberConfig{
			ChatID: chatID,
			UserID: userID,
		},
		UntilDate: 0,
	}
	_, err := bot.KickChatMember(memberConfig)
	return err
}

// https://text.ru/api-check/manual
func checkUniqueness(voteModel models.Vote) bool {
	token := config.TextRuToken
	if len(config.TextRuToken) == 0 {
		return
	}

	text = strip.StripTags(text)

	if len(text) < config.MinimumPostLength {
		return
	}

	cut := func(text string, to int) string {
		runes := []rune(text)
		if len(runes) > to {
			return string(runes[:to])
		}
		return text
	}
	maxSymbolCount := 2000
	text = cut(text, maxSymbolCount)

	httpClient := http.Client{}
	form := url.Values{}
	form.Add("text", text)
	form.Add("userkey", token)
	domainList := strings.Join(config.Domains, ",")
	form.Add("exceptdomain", domainList)
	form.Add("visible", "vis_on")
	req, err := http.NewRequest("POST", "http://api.text.ru/post", strings.NewReader(form.Encode()))
	if err != nil {
		log.Println(err.Error())
		return
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		log.Println(err.Error())
		return
	}
	if resp.StatusCode != 200 {
		log.Println("статус не 200")
		return
	}
	type Uid struct {
		TextUid string `json:"text_uid"`
	}
	var uid Uid
	jsonParser := json.NewDecoder(resp.Body)
	jsonParser.Decode(&uid)
	if len(uid.TextUid) == 0 {
		log.Println("Не распарсили text_uid")
		return
	}
	step := 0
	for step < 50 {
		step += 1
		time.Sleep(time.Second * 15)
		log.Printf("step %d", step)
		client := http.Client{}
		form := url.Values{}
		form.Add("uid", uid.TextUid)
		form.Add("userkey", token)
		//form.Add("jsonvisible", "detail")
		req, err := http.NewRequest("POST", "http://api.text.ru/post", strings.NewReader(form.Encode()))
		req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			log.Println(err.Error())
			return
		}
		type Result struct {
			TextUnique string `json:"text_unique"`
			ResultJson string `json:"result_json"`
		}
		var result Result
		jsonParser := json.NewDecoder(resp.Body)
		jsonParser.Decode(&result)
		if len(result.TextUnique) == 0 {
			continue
		}
		textUnique, err := strconv.ParseFloat(result.TextUnique, 32)
		if err != nil {
			log.Println(err.Error())
			return
		}
		log.Println(textUnique)
		if textUnique < 20 {
			voteModel.Completed = true
			_, err := voteModel.Save(database)
			if err != nil {
				log.Println(err.Error())
				return
			}
			return false
			}
		} else {
			random := func(min, max int) int {
				rand.Seed(time.Now().Unix())
				return rand.Intn(max-min) + min
			}
			imageNumber := random(1, 18)
			report := fmt.Sprintf("[![Уникальность проверена через TEXT.RU](https://text.ru/image/get/%s/%d)](https://text.ru/antiplagiat/%s)",
				uid.TextUid, imageNumber, uid.TextUid)
			err = sendComment(voteModel.Author, voteModel.Permalink, report)
			if err != nil {
				log.Println(err.Error())
			}
			return true
		}
		// если дошли сюда, то выходим из цикла
		break
	}
}

func sendComment(author string, permalink string, text string) error {
	golos := golosClient.NewApi(config.Rpc, config.Chain)
	defer golos.Rpc.Close()
	vote := golosClient.PC_Vote{Weight: 100 * 100}
	options := golosClient.PC_Options{Percent: 50}
	err := golos.Comment(
		config.Account,
		author,
		permalink,
		text,
		&vote,
		&options)
	return err
}

func vote(voteModel models.Vote, chatID int64, messageID int) {
	credentials, err := models.GetAllActiveCredentials(database)
	if err != nil {
		log.Println("Не смогли извлечь ключи из базы")
		return
	}
	for _, credential := range credentials {
		if config.Account != credential.UserName {
			golosClient.Key_List[credential.UserName] = golosClient.Keys{PKey: config.PostingKey}
		}
	}
	log.Printf("Голосую за пост %s/%s, загружено %d аккаунтов", voteModel.Author, voteModel.Permalink, len(credentials))
	var errors []error
	var wg sync.WaitGroup
	wg.Add(len(credentials))
	for _, credential := range credentials {
		go func(credential models.Credential) {
			defer wg.Done()
			weight := credential.Power * 100
			golos := golosClient.NewApi(config.Rpc, config.Chain)
			defer golos.Rpc.Close()
			err := golos.Vote(credential.UserName, voteModel.Author, voteModel.Permalink, weight)
			if err != nil {
				log.Println("Ошибка при голосовании: " + err.Error())
				errors = append(errors, err)
			}
		}(credential)
	}
	wg.Wait()
	successVotesCount := len(credentials) - len(errors)
	text := fmt.Sprintf("Успешно проголосовала c %d аккаунтов", successVotesCount)
	if err != nil {
		log.Println(err.Error())
		text = fmt.Sprintf("В процессе голосования произошла ошибка, свяжитесь с разработчиком - %s", config.Developer)
	}
	log.Println(text)
	msg := tgbotapi.NewEditMessageText(chatID, messageID, "")
	msg.Text = text
	_, err = bot.Send(msg)
	if err != nil {
		log.Println(err.Error())
	}
}

func sendReferralFee(referrer string, referral string) {
	if referrer == referral {
		log.Printf("Пригласивший и приглашенный %s совпадают", referral)
		return
	}
	golos := golosClient.NewApi(config.Rpc, config.Chain)
	defer golos.Rpc.Close()
	accounts, err := golos.Rpc.Database.GetAccounts([]string{referral})
	if err != nil {
		log.Println("Не получили аккаунт " + referral)
		return
	}
	const minPostCount int64 = 30
	if accounts[0].PostCount.Int64() < minPostCount {
		log.Printf("За новичка %s награды не будет, слишком мало постов", referral)
		return
	}
	amount := fmt.Sprintf("%.3f GOLOS", config.ReferralFee)
	err = golos.TransferToVesting(config.Account, referrer, amount)
	err2 := golos.TransferToVesting(config.Account, referral, amount)
	if err != nil {
		log.Println(fmt.Sprintf("Не отправили силу голоса %s \nаккаунту %s", err.Error(), referrer))
	}
	if err2 != nil {
		log.Println(fmt.Sprintf("Не отправили силу голоса %s \nаккаунту %s", err.Error(), referral))
	}
	if err != nil || err2 != nil {
		return
	}
	markdownLink := func(account string) string {
		return fmt.Sprintf("[@%s](https://golos.io/@%s/transfers)", account, account)
	}
	referrerLink := markdownLink(referrer)
	referralLink := markdownLink(referral)
	text := fmt.Sprintf("Пригласивший %s и приглашённый %s получают по %.3f Силы Голоса в рамках партнёрской программы",
		referrerLink, referralLink, config.ReferralFee)
	msg := tgbotapi.NewMessage(config.GroupID, text)
	msg.ParseMode = "Markdown"
	_, err = bot.Send(msg)
	if err != nil {
		log.Println("Не отправили сообщение: " + err.Error())
	}
}

func checkAuthority() {
	for {
		credentials, err := models.GetAllActiveCredentials(database)
		if err != nil {
			log.Println("checkAuthority() failed")
		}
		golos := golosClient.NewApi(config.Rpc, config.Chain)
		defer golos.Rpc.Close()
		for _, credential := range credentials {
			credential.Active = golos.Verify_Delegate_Posting_Key_Sign(credential.UserName, config.Account)
			_, _ = credential.Save(database)
   		}
		time.Sleep(time.Hour)
	}
}

func newPost(voteID int, author string, permalink string, chatID int) {
	curatorChatIDs := models.GetAllActiveCurstorsChatID(database)
	curateText := "Новый пост - новая оценка. Курируй, куратор\n" + helpers.GetInstantViewLink(author, permalink)
	for _, curatorChatID := range curatorChatIDs {
		if curatorChatID == chatID {
			continue
		}
		msg := tgbotapi.NewMessage(curatorChatID, update.Message.Text)
		markup := helpers.GetVoteMarkup(voteID, 0, 0)
		msg.ReplyMarkup = markup
		msg.DisableWebPagePreview = false
		
		message, err := bot.Send(msg)
		if err != nil {
			log.Println("Не смогли отправить сообщение куратору " + curatorChatID)
		}		
	}
}

func queueProcessor()
{
	for i := 0; i != nil; i++ {
		
	}
}
