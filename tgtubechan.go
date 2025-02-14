/*

https://github.com/kkdai/youtube/v2/

https://developers.google.com/youtube/v3/docs/playlistItems/list
https://console.cloud.google.com/apis/api/youtube.googleapis.com/quotas

https://core.telegram.org/bots/api


go get github.com/kkdai/youtube/v2@master

GoGet
GoFmt
GoBuildNull

*/

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	ytdl "github.com/kkdai/youtube/v2"
	youtubeoption "google.golang.org/api/option"
	youtube "google.golang.org/api/youtube/v3"
	yaml "gopkg.in/yaml.v3"
)

const (
	NL = "\n"

	BEAT = time.Duration(24) * time.Hour / 1000
)

type TgTubeChanConfig struct {
	YssUrl string `yaml:"-"`

	DEBUG bool `yaml:"DEBUG"`

	Interval time.Duration `yaml:"Interval"`

	TgApiUrlBase string `yaml:"TgApiUrlBase"` // = "https://api.telegram.org"

	TgToken        string `yaml:"TgToken"`
	TgChatId       string `yaml:"TgChatId"`
	TgBossChatId   string `yaml:"TgBossChatId"`
	TgPerformer    string `yaml:"TgPerformer"`
	TgTitleCleanRe string `yaml:"TgTitleCleanRe"`
	TgTitleUnquote bool   `yaml:"TgTitleUnquote"`

	TgVideosInterval time.Duration `yaml:"TgVideosInterval"`

	TgAudioBitrateKbps int64 `yaml:"TgAudioBitrateKbps"` // = 60

	YtKey             string `yaml:"YtKey"`
	YtMaxResults      int64  `yaml:"YtMaxResults"` // = 50
	YtUsername        string `yaml:"YtUsername"`
	YtChannelId       string `yaml:"YtChannelId"`
	YtPlaylistId      string `yaml:"YtPlaylistId"`
	YtLastPublishedAt string `yaml:"YtLastPublishedAt"`

	YtHttpClientUserAgent string `yaml:"YtHttpClientUserAgent"` // = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.2 Safari/605.1.15"

	FfmpegPath          string   `yaml:"FfmpegPath"`          // = "/bin/ffmpeg"
	FfmpegGlobalOptions []string `yaml:"FfmpegGlobalOptions"` // = []string{"-v", "panic"}
}

var (
	Config TgTubeChanConfig

	Ctx context.Context

	HttpClient = &http.Client{}

	YtdlCl ytdl.Client
	YtSvc  *youtube.Service

	TgTitleCleanRe *regexp.Regexp
)

func init() {
	var err error

	Ctx = context.TODO()

	if v := os.Getenv("YssUrl"); v != "" {
		Config.YssUrl = v
	}
	if Config.YssUrl == "" {
		log("ERROR YssUrl empty")
		os.Exit(1)
	}

	if err := Config.Get(); err != nil {
		log("ERROR Config.Get: %v", err)
		os.Exit(1)
	}

	if Config.DEBUG {
		log("DEBUG==true")
	}

	log("Interval==%v", Config.Interval)
	if Config.Interval == 0 {
		log("ERROR Interval empty")
		os.Exit(1)
	}

	log("TgApiUrlBase==`%s`", Config.TgApiUrlBase)
	if Config.TgApiUrlBase == "" {
		log("ERROR TgApiUrlBase empty")
		os.Exit(1)
	}

	if Config.TgToken == "" {
		log("ERROR TgToken empty")
		os.Exit(1)
	}

	if Config.TgBossChatId == "" {
		log("ERROR TgBossChatId empty")
		os.Exit(1)
	}

	if Config.TgChatId == "" {
		tglog("ERROR TgChatId empty")
		os.Exit(1)
	}

	if Config.TgVideosInterval == 0 {
		log("ERROR TgVideosInterval empty")
		os.Exit(1)
	}

	if Config.TgAudioBitrateKbps == 0 {
		log("ERROR TgAudioBitrateKbps empty")
		os.Exit(1)
	}

	if Config.TgTitleCleanRe != "" {
		TgTitleCleanRe, err = regexp.Compile(Config.TgTitleCleanRe)
		if err != nil {
			log("ERROR TgTitleCleanRe `%s`; %s", Config.TgTitleCleanRe, err)
			os.Exit(1)
		}
	}

	if Config.YtKey == "" {
		tglog("ERROR YtKey empty")
		os.Exit(1)
	}

	if Config.YtMaxResults == 0 {
		tglog("ERROR YtMaxResults empty")
		os.Exit(1)
	}

	log("FfmpegPath==`%s`", Config.FfmpegPath)
	log("FfmpegGlobalOptions==%+v", Config.FfmpegGlobalOptions)
}

func main() {
	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM)
	go func(sigterm chan os.Signal) {
		<-sigterm
		tglog("%s: sigterm", os.Args[0])
		os.Exit(1)
	}(sigterm)

	for {
		t0 := time.Now()

		YtdlCl = ytdl.Client{
			HTTPClient: &http.Client{
				Transport: &UserAgentTransport{
					http.DefaultTransport,
					Config.YtHttpClientUserAgent,
				},
			},
		}
		processYtChannel()

		if dur := time.Now().Sub(t0); dur < Config.Interval {
			time.Sleep(Config.Interval - dur)
		}
	}

	return
}

func beats(td time.Duration) int {
	return int(td / BEAT)
}

func ts() string {
	t := time.Now().Local()
	return fmt.Sprintf(
		"%03d:"+"%02d%02d:"+"%02d%02d",
		t.Year()%1000, t.Month(), t.Day(), t.Hour(), t.Minute(),
	)
}

func log(msg interface{}, args ...interface{}) {
	msgtext := fmt.Sprintf("%s %s", ts(), msg) + NL
	fmt.Fprintf(os.Stderr, msgtext, args...)
}

func processYtChannel() {
	var err error

	YtSvc, err = youtube.NewService(Ctx, youtubeoption.WithAPIKey(Config.YtKey))
	if err != nil {
		tglog("ERROR youtube NewService: %s", err)
		os.Exit(1)
	}

	if Config.YtPlaylistId == "" {
		if Config.YtUsername == "" && Config.YtChannelId == "" {
			tglog("Empty YtUsername and YtChannelId, nothing to do")
			os.Exit(1)
		}

		// https://developers.google.com/youtube/v3/docs/channels/list

		channelslistcall := YtSvc.Channels.List([]string{"id", "snippet", "contentDetails"}).MaxResults(11)
		if Config.YtChannelId != "" {
			channelslistcall = channelslistcall.Id(Config.YtChannelId)
		} else if Config.YtUsername != "" {
			channelslistcall = channelslistcall.ForUsername(Config.YtUsername)
		}
		channelslist, err := channelslistcall.Do()
		if err != nil {
			tglog("ERROR channels/list: %v", err)
			os.Exit(1)
		}

		if len(channelslist.Items) == 0 {
			tglog("ERROR channels/list: empty result")
			os.Exit(1)
		}
		for _, c := range channelslist.Items {
			tglog(
				"channel id: %s"+NL+"channel title: %s"+NL+"uploads playlist id: %+v"+NL,
				c.Id, c.Snippet.Title, c.ContentDetails.RelatedPlaylists.Uploads,
			)
		}
		if len(channelslist.Items) > 1 {
			tglog("ERROR channels/list: more than one result")
			os.Exit(1)
		}
		Config.YtPlaylistId = channelslist.Items[0].ContentDetails.RelatedPlaylists.Uploads
	}

	// https://developers.google.com/youtube/v3/docs/playlistItems/list

	var videos []youtube.PlaylistItemSnippet

	playlistitemslistcall := YtSvc.PlaylistItems.List([]string{"id", "snippet", "contentDetails"}).MaxResults(Config.YtMaxResults)
	playlistitemslistcall = playlistitemslistcall.PlaylistId(Config.YtPlaylistId)
	err = playlistitemslistcall.Pages(
		Ctx,
		func(r *youtube.PlaylistItemListResponse) error {
			for _, i := range r.Items {
				if Config.YtLastPublishedAt == "" || i.Snippet.PublishedAt > Config.YtLastPublishedAt {
					videos = append(videos, *i.Snippet)
				}
			}
			return nil
		},
	)
	if err != nil {
		tglog("ERROR playlistitems/list: %v", err)
		os.Exit(1)
	}

	if len(videos) > 0 {
		log("DEBUG playlistitems/list: %d items", len(videos))
	}

	sort.Slice(videos, func(i, j int) bool { return videos[i].PublishedAt < videos[j].PublishedAt })

	for j, v := range videos {
		tglog(
			"DEBUG "+NL+"%d/%d "+NL+"%s "+NL+"youtu.be/%s "+NL+"%s ",
			j+1,
			len(videos),
			v.Title,
			v.ResourceId.VideoId,
			v.PublishedAt,
		)

		var vpatime time.Time
		var vtitle string
		var audioName string

		vpatime, err = time.Parse(time.RFC3339, v.PublishedAt)
		if err != nil {
			tglog("ERROR time.Parse PublishedAt: %v", err)
			os.Exit(1)
		}

		vtitle = v.Title
		if TgTitleCleanRe != nil {
			vtitle = TgTitleCleanRe.ReplaceAllString(vtitle, "")
		}
		if Config.TgTitleUnquote {
			if strings.HasPrefix(vtitle, `"`) && strings.HasSuffix(vtitle, `"`) {
				vtitle = strings.Trim(vtitle, `"`)
			}
			if strings.HasPrefix(vtitle, `«`) && strings.HasSuffix(vtitle, `»`) {
				vtitle = strings.Trim(vtitle, `«`)
				vtitle = strings.Trim(vtitle, `»`)
			}
			for strings.Contains(vtitle, `"`) {
				vtitle = strings.Replace(vtitle, `"`, `«`, 1)
				vtitle = strings.Replace(vtitle, `"`, `»`, 1)
			}
		}

		audioName = fmt.Sprintf(
			"%04d%02d%02d.%02d%02d%02d.%s",
			vpatime.Year(), vpatime.Month(), vpatime.Day(),
			vpatime.Hour(), vpatime.Minute(), vpatime.Second(),
			v.ResourceId.VideoId,
		)

		var coverUrl, thumbUrl string
		var coverBuf, thumbBuf, audioBuf *bytes.Buffer

		if v.Thumbnails.Maxres != nil && v.Thumbnails.Maxres.Url != "" {
			coverUrl = v.Thumbnails.Maxres.Url
		} else if v.Thumbnails.Standard != nil && v.Thumbnails.Standard.Url != "" {
			coverUrl = v.Thumbnails.Standard.Url
		} else if v.Thumbnails.High != nil && v.Thumbnails.High.Url != "" {
			coverUrl = v.Thumbnails.High.Url
		} else if v.Thumbnails.Medium != nil && v.Thumbnails.Medium.Url != "" {
			coverUrl = v.Thumbnails.Medium.Url
		} else {
			tglog("ERROR no cover url")
			break
		}

		if v.Thumbnails.Medium != nil && v.Thumbnails.Medium.Url != "" {
			thumbUrl = v.Thumbnails.Medium.Url
		} else {
			tglog("ERROR no thumb url")
			break
		}

		coverBuf, err = downloadFile(coverUrl)
		if err != nil {
			tglog("ERROR download cover: %v", err)
			break
		}
		log("DEBUG cover: %dkb", coverBuf.Len()/1000)

		thumbBuf, err = downloadFile(thumbUrl)
		if err != nil {
			tglog("ERROR download thumb: %v", err)
			break
		}
		log("DEBUG thumb: %dkb", thumbBuf.Len()/1000)

		vinfo, err := YtdlCl.GetVideoContext(Ctx, v.ResourceId.VideoId)
		if err != nil {
			tglog("ERROR GetVideoContext: %v", err)
			// 23/5@415 New: #216 20221215.091855.8Q8QCOlhn5U: Прямая трансляция пользователя Сергей Бугаев
			// 23/5@415 GetVideoContext: cannot playback and download, status: LIVE_STREAM_OFFLINE, reason: This live event will begin in a few moments.
			break
		}

		var audioFormat ytdl.Format
		for _, f := range vinfo.Formats {
			if !strings.HasPrefix(f.MimeType, "audio/mp4") {
				continue
			}
			if audioFormat.Bitrate == 0 || f.Bitrate > audioFormat.Bitrate {
				audioFormat = f
			}
		}

		ytstream, _, err := YtdlCl.GetStreamContext(Ctx, vinfo, &audioFormat)
		if err != nil {
			tglog("ERROR GetStreamContext: %v", err)
			break
		}
		defer ytstream.Close()

		audioBuf = bytes.NewBuffer(nil)
		_, err = io.Copy(audioBuf, ytstream)
		if err != nil {
			tglog("ERROR copy stream: %v", err)
			break
		}

		log(
			"Downloaded audio size:%dmb bitrate:%dkbps duration:%ds",
			audioBuf.Len()/1000/1000,
			audioFormat.Bitrate/1024,
			int64(vinfo.Duration.Seconds()),
		)
		if audioBuf.Len()/1000/1000 < 1 {
			log("WARNING Downloaded audio is less than one megabyte, aborting.")
			break
		}

		audioSrcFile := fmt.Sprintf("%s..m4a", audioName)
		err = ioutil.WriteFile(audioSrcFile, audioBuf.Bytes(), 0400)
		if err != nil {
			tglog("ERROR WriteFile %s: %v", audioSrcFile, err)
			break
		}

		audioFile := audioSrcFile

		if Config.FfmpegPath != "" && Config.TgAudioBitrateKbps > 0 {
			log("DEBUG target audio bitrate:%dkbps", Config.TgAudioBitrateKbps)
			audioFile = fmt.Sprintf("%s..%dk..m4a", audioName, Config.TgAudioBitrateKbps)
			ffmpegArgs := append(Config.FfmpegGlobalOptions,
				"-i", audioSrcFile,
				"-b:a", fmt.Sprintf("%dk", Config.TgAudioBitrateKbps),
				audioFile,
			)
			err = exec.Command(Config.FfmpegPath, ffmpegArgs...).Run()
			if err != nil {
				tglog("ERROR ffmpeg: %v", err)
				break
			}

			err = os.Remove(audioSrcFile)
			if err != nil {
				tglog("ERROR Remove %s: %v", audioSrcFile, err)
			}
		}

		abb, err := ioutil.ReadFile(audioFile)
		if err != nil {
			tglog("ERROR ReadFile %s: %v", audioFile, err)
			break
		}
		audioBuf = bytes.NewBuffer(abb)

		log("audio size:%dmb", audioBuf.Len()/1000/1000)

		err = os.Remove(audioFile)
		if err != nil {
			tglog("ERROR Remove %s: %v", audioFile, err)
		}

		tgcover, err := tgsendPhotoFile(audioName, coverBuf, vtitle)
		if err != nil {
			tglog("ERROR tgsendPhotoFile: %v", err)
			break
		}
		if tgcover.FileId == "" {
			tglog("ERROR tgsendPhotoFile: file_id empty")
			break
		}

		tgaudio, err := tgsendAudioFile(
			Config.TgPerformer,
			vtitle,
			audioName,
			audioBuf,
			thumbBuf,
			vinfo.Duration,
		)
		if err != nil {
			tglog("ERROR tgsendAudioFile: %v", err)
			break
		}
		if tgaudio.FileId == "" {
			tglog("ERROR tgsendAudioFile: file_id empty")
			break
		}

		_, err = tgsendPhoto(tgcover.FileId, vtitle)
		if err != nil {
			tglog("ERROR tgsendPhoto: %v", err)
			break
		}

		_, err = tgsendAudio(
			tgaudio.FileId,
			fmt.Sprintf("%s "+NL+"%s "+NL+"youtu.be/%s %s ", vtitle, Config.TgPerformer, v.ResourceId.VideoId, vinfo.Duration),
		)
		if err != nil {
			tglog("ERROR tgsendAudio: %v", err)
			break
		}

		var spp []string
		if len(v.Description) < 4000 {
			spp = []string{v.Description}
		} else {
			var sp string
			srs := strings.Split(v.Description, NL+NL)
			for i, s := range srs {
				sp += s + NL + NL
				if i == len(srs)-1 || len(sp)+len(srs[i+1]) > 4000 {
					spp = append(spp, sp)
					sp = ""
				}
			}
		}

		for _, sp := range spp {
			if strings.TrimSpace(sp) == "" {
				continue
			}
			_, err = tgsendMessage(sp)
			if err != nil {
				tglog("ERROR tgsendMessage: %v", err)
				break
			}
		}

		Config.YtLastPublishedAt = vpatime.Format(time.RFC3339)
		if err := Config.Put(); err != nil {
			log("ERROR Config.Put: %s", err)
			break
		}

		if len(videos) > 10 {
			log("sleeping %v", Config.TgVideosInterval)
			time.Sleep(Config.TgVideosInterval)
		}
	}

	return
}

func tglog(msg interface{}, args ...interface{}) error {
	log(msg, args...)
	msgtext := fmt.Sprintf(fmt.Sprintf("%s", msg), args...) + NL

	smreq := TgSendMessageRequest{
		ChatId:                Config.TgBossChatId,
		Text:                  msgtext,
		ParseMode:             "",
		DisableWebPagePreview: true,
		DisableNotification:   true,
	}
	smreqjs, err := json.Marshal(smreq)
	if err != nil {
		return fmt.Errorf("tglog json marshal: %w", err)
	}
	smreqjsBuffer := bytes.NewBuffer(smreqjs)

	var resp *http.Response
	tgapiurl := fmt.Sprintf("%s/bot%s/sendMessage", Config.TgApiUrlBase, Config.TgToken)
	resp, err = http.Post(
		tgapiurl,
		"application/json",
		smreqjsBuffer,
	)
	if err != nil {
		return fmt.Errorf("tglog apiurl:`%s` apidata:`%s`: %w", tgapiurl, smreqjs, err)
	}

	var smresp TgSendMessageResponse
	err = json.NewDecoder(resp.Body).Decode(&smresp)
	if err != nil {
		return fmt.Errorf("tglog decode response: %w", err)
	}
	if !smresp.OK {
		return fmt.Errorf("tglog apiurl:`%s` apidata:`%s` api response not ok: %+v", tgapiurl, smreqjs, smresp)
	}

	return nil
}

type TgSendMessageRequest struct {
	ChatId                string `json:"chat_id"`
	Text                  string `json:"text"`
	ParseMode             string `json:"parse_mode,omitempty"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
	DisableNotification   bool   `json:"disable_notification"`
}

type TgSendMessageResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	Result      struct {
		MessageId int64 `json:"message_id"`
	} `json:"result"`
}

type TgResponse struct {
	Ok          bool       `json:"ok"`
	Description string     `json:"description"`
	Result      *TgMessage `json:"result"`
}

type TgResponseShort struct {
	Ok          bool   `json:"ok"`
	Description string `json:"description"`
}

type TgPhotoSize struct {
	FileId       string `json:"file_id"`
	FileUniqueId string `json:"file_unique_id"`
	Width        int64  `json:"width"`
	Height       int64  `json:"height"`
	FileSize     int64  `json:"file_size"`
}

type TgAudio struct {
	FileId       string      `json:"file_id"`
	FileUniqueId string      `json:"file_unique_id"`
	Duration     int64       `json:"duration"`
	Performer    string      `json:"performer"`
	Title        string      `json:"title"`
	MimeType     string      `json:"mime_type"`
	FileSize     int64       `json:"file_size"`
	Thumb        TgPhotoSize `json:"thumb"`
}

type TgMessage struct {
	Id        string
	MessageId int64         `json:"message_id"`
	Audio     TgAudio       `json:"audio"`
	Photo     []TgPhotoSize `json:"photo"`
}

func tgsendMessage(message string) (msg *TgMessage, err error) {
	sendMessage := TgSendMessageRequest{
		ChatId:                Config.TgChatId,
		Text:                  message,
		DisableWebPagePreview: true,
	}
	sendMessageJSON, err := json.Marshal(sendMessage)
	if err != nil {
		return nil, err
	}

	var tgresp TgResponse
	err = postJson(
		fmt.Sprintf("%s/bot%s/sendMessage", Config.TgApiUrlBase, Config.TgToken),
		bytes.NewBuffer(sendMessageJSON),
		&tgresp,
	)
	if err != nil {
		return nil, err
	}

	if !tgresp.Ok {
		return nil, fmt.Errorf("sendMessage: %s", tgresp.Description)
	}

	msg = tgresp.Result
	msg.Id = fmt.Sprintf("%d", msg.MessageId)

	return msg, nil
}

func tgdeleteMessage(messageid int64) error {
	deleteMessage := map[string]interface{}{
		"chat_id":    Config.TgChatId,
		"message_id": messageid,
	}
	deleteMessageJSON, err := json.Marshal(deleteMessage)
	if err != nil {
		return err
	}

	var tgresp TgResponseShort
	err = postJson(
		fmt.Sprintf("%s/bot%s/deleteMessage", Config.TgApiUrlBase, Config.TgToken),
		bytes.NewBuffer(deleteMessageJSON),
		&tgresp,
	)
	if err != nil {
		return fmt.Errorf("postJson: %v", err)
	}

	if !tgresp.Ok {
		return fmt.Errorf("deleteMessage: %s", tgresp.Description)
	}

	return nil
}

func tgsendAudioFile(performer, title string, fileName string, audioBuf, thumbBuf *bytes.Buffer, duration time.Duration) (audio *TgAudio, err error) {
	// https://core.telegram.org/bots/API#sending-files

	var mpartBuf bytes.Buffer
	mpart := multipart.NewWriter(&mpartBuf)
	var formWr io.Writer

	// chat_id
	formWr, err = mpart.CreateFormField("chat_id")
	if err != nil {
		return nil, fmt.Errorf("CreateFormField(`chat_id`): %v", err)
	}
	_, err = formWr.Write([]byte(Config.TgChatId))
	if err != nil {
		return nil, fmt.Errorf("Write(chat_id): %v", err)
	}

	// performer
	formWr, err = mpart.CreateFormField("performer")
	if err != nil {
		return nil, fmt.Errorf("CreateFormField(`performer`): %v", err)
	}
	_, err = formWr.Write([]byte(performer))
	if err != nil {
		return nil, fmt.Errorf("Write(performer): %v", err)
	}

	// title
	formWr, err = mpart.CreateFormField("title")
	if err != nil {
		return nil, fmt.Errorf("CreateFormField(`title`): %v", err)
	}
	_, err = formWr.Write([]byte(title))
	if err != nil {
		return nil, fmt.Errorf("Write(title): %v", err)
	}

	// audio
	formWr, err = mpart.CreateFormFile("audio", fileName)
	if err != nil {
		return nil, fmt.Errorf("CreateFormFile('audio'): %v", err)
	}
	_, err = io.Copy(formWr, audioBuf)
	if err != nil {
		return nil, fmt.Errorf("Copy audio: %v", err)
	}

	// thumb
	formWr, err = mpart.CreateFormFile("thumb", fileName+".thumb")
	if err != nil {
		return nil, fmt.Errorf("CreateFormFile(`thumb`): %v", err)
	}
	_, err = io.Copy(formWr, thumbBuf)
	if err != nil {
		return nil, fmt.Errorf("Copy thumb: %v", err)
	}

	// duration
	formWr, err = mpart.CreateFormField("duration")
	if err != nil {
		return nil, fmt.Errorf("CreateFormField(`duration`): %v", err)
	}
	_, err = formWr.Write([]byte(strconv.Itoa(int(duration.Seconds()))))
	if err != nil {
		return nil, fmt.Errorf("Write(duration): %v", err)
	}

	err = mpart.Close()
	if err != nil {
		return nil, fmt.Errorf("multipartWriter.Close: %v", err)
	}

	resp, err := HttpClient.Post(
		fmt.Sprintf("%s/bot%s/sendAudio", Config.TgApiUrlBase, Config.TgToken),
		mpart.FormDataContentType(),
		&mpartBuf,
	)
	if err != nil {
		return nil, fmt.Errorf("Post: %v", err)
	}
	defer resp.Body.Close()

	var tgresp TgResponse
	err = json.NewDecoder(resp.Body).Decode(&tgresp)
	if err != nil {
		return nil, fmt.Errorf("Decode: %v", err)
	}
	if !tgresp.Ok {
		return nil, fmt.Errorf("sendAudio: %s", tgresp.Description)
	}

	msg := tgresp.Result
	msg.Id = fmt.Sprintf("%d", msg.MessageId)

	audio = &msg.Audio

	if audio.FileId == "" {
		return nil, fmt.Errorf("sendAudio: Audio.FileId empty")
	}

	err = tgdeleteMessage(msg.MessageId)
	if err != nil {
		return nil, fmt.Errorf("tgdeleteMessage(%d): %v", msg.MessageId, err)
	}

	return audio, nil
}

func tgsendAudio(fileid string, caption string) (msg *TgMessage, err error) {
	// https://core.telegram.org/bots/API#sendaudio

	sendAudio := map[string]interface{}{
		"chat_id": Config.TgChatId,
		"audio":   fileid,
		"caption": caption,
	}
	sendAudioJSON, err := json.Marshal(sendAudio)
	if err != nil {
		return nil, err
	}

	var tgresp TgResponse
	err = postJson(
		fmt.Sprintf("%s/bot%s/sendAudio", Config.TgApiUrlBase, Config.TgToken),
		bytes.NewBuffer(sendAudioJSON),
		&tgresp,
	)
	if err != nil {
		return nil, err
	}

	if !tgresp.Ok {
		return nil, fmt.Errorf("sendAudio: %s", tgresp.Description)
	}

	msg = tgresp.Result
	msg.Id = fmt.Sprintf("%d", msg.MessageId)

	return msg, nil
}

func tgsendPhotoFile(fileName string, photoBuf *bytes.Buffer, caption string) (photo *TgPhotoSize, err error) {
	var mpartBuf bytes.Buffer
	mpart := multipart.NewWriter(&mpartBuf)
	var formWr io.Writer

	// chat_id
	formWr, err = mpart.CreateFormField("chat_id")
	if err != nil {
		return nil, fmt.Errorf("CreateFormField(`chat_id`): %v", err)
	}
	_, err = formWr.Write([]byte(Config.TgChatId))
	if err != nil {
		return nil, fmt.Errorf("Write(chat_id): %v", err)
	}

	// photo
	formWr, err = mpart.CreateFormFile("photo", fileName+".cover")
	if err != nil {
		return nil, fmt.Errorf("CreateFormFile(`photo`): %v", err)
	}
	_, err = io.Copy(formWr, photoBuf)
	if err != nil {
		return nil, fmt.Errorf("Copy photo: %v", err)
	}

	err = mpart.Close()
	if err != nil {
		return nil, fmt.Errorf("multipartWriter.Close: %v", err)
	}

	resp, err := HttpClient.Post(
		fmt.Sprintf("%s/bot%s/sendPhoto", Config.TgApiUrlBase, Config.TgToken),
		mpart.FormDataContentType(),
		&mpartBuf,
	)
	if err != nil {
		return nil, fmt.Errorf("Post: %v", err)
	}
	defer resp.Body.Close()

	var tgresp TgResponse
	err = json.NewDecoder(resp.Body).Decode(&tgresp)
	if err != nil {
		return nil, fmt.Errorf("Decode: %v", err)
	}
	if !tgresp.Ok {
		return nil, fmt.Errorf("sendPhoto: %s", tgresp.Description)
	}

	msg := tgresp.Result
	msg.Id = fmt.Sprintf("%d", msg.MessageId)

	if len(msg.Photo) == 0 {
		return nil, fmt.Errorf("sendPhoto: Photo empty")
	}

	photo = &TgPhotoSize{}
	for _, p := range msg.Photo {
		if p.Width > photo.Width {
			photo = &p
		}
	}

	if photo.FileId == "" {
		return nil, fmt.Errorf("sendPhoto: Photo.FileId empty")
	}

	err = tgdeleteMessage(msg.MessageId)
	if err != nil {
		return nil, fmt.Errorf("tgdeleteMessage(%d): %v", msg.MessageId, err)
	}

	return photo, nil
}

func tgsendPhoto(fileid, caption string) (msg *TgMessage, err error) {
	caption = fmt.Sprintf("<u><b>%s</b></u>", caption)
	sendPhoto := map[string]interface{}{
		"chat_id":    Config.TgChatId,
		"photo":      fileid,
		"caption":    caption,
		"parse_mode": "HTML",
	}
	sendPhotoJSON, err := json.Marshal(sendPhoto)
	if err != nil {
		return nil, err
	}

	var tgresp TgResponse
	err = postJson(
		fmt.Sprintf("%s/bot%s/sendPhoto", Config.TgApiUrlBase, Config.TgToken),
		bytes.NewBuffer(sendPhotoJSON),
		&tgresp,
	)
	if err != nil {
		return nil, err
	}

	if !tgresp.Ok {
		return nil, fmt.Errorf("sendPhoto: %s", tgresp.Description)
	}

	msg = tgresp.Result
	msg.Id = fmt.Sprintf("%d", msg.MessageId)

	return msg, nil
}

func getJson(url string, target interface{}) error {
	r, err := HttpClient.Get(url)
	if err != nil {
		return err
	}
	defer r.Body.Close()

	return json.NewDecoder(r.Body).Decode(target)
}

func postJson(url string, data *bytes.Buffer, target interface{}) error {
	resp, err := HttpClient.Post(
		url,
		"application/json",
		data,
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody := bytes.NewBuffer(nil)
	_, err = io.Copy(respBody, resp.Body)
	if err != nil {
		return fmt.Errorf("io.Copy: %v", err)
	}

	err = json.NewDecoder(respBody).Decode(target)
	if err != nil {
		return fmt.Errorf("Decode: %v", err)
	}

	return nil
}

func downloadFile(url string) (*bytes.Buffer, error) {
	resp, err := HttpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var bb = bytes.NewBuffer(nil)

	_, err = io.Copy(bb, resp.Body)
	if err != nil {
		return nil, err
	}

	return bb, nil
}

func (config *TgTubeChanConfig) Get() error {
	req, err := http.NewRequest(http.MethodGet, config.YssUrl, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("yss response status %s", resp.Status)
	}

	rbb, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if err := yaml.Unmarshal(rbb, config); err != nil {
		return err
	}

	if config.DEBUG {
		log("DEBUG Config.Get: %+v", config)
	}

	return nil
}

func (config *TgTubeChanConfig) Put() error {
	if config.DEBUG {
		log("DEBUG Config.Put %s %+v", config.YssUrl, config)
	}

	rbb, err := yaml.Marshal(config)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPut, config.YssUrl, bytes.NewBuffer(rbb))
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("yss response status %s", resp.Status)
	}

	return nil
}

type UserAgentTransport struct {
	Transport http.RoundTripper
	UserAgent string
}

func (uat *UserAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", uat.UserAgent)
	return uat.Transport.RoundTrip(req)
}
