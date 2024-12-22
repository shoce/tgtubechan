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
	"crypto/tls"
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

	youtubedl "github.com/kkdai/youtube/v2"
	etcd "go.etcd.io/etcd/client/v3"
	youtubeoption "google.golang.org/api/option"
	youtube "google.golang.org/api/youtube/v3"
	yaml "gopkg.in/yaml.v3"
)

const (
	NL = "\n"

	BEAT = time.Duration(24) * time.Hour / 1000
)

var (
	DEBUG bool

	Interval time.Duration

	YamlConfigPath = "tgtubechan.yaml"

	EtcdEndpoint     string = "etcd:2379"
	EtcdRootPassword string
	EtcdKeyPrefix    string

	// https://pkg.go.dev/go.etcd.io/etcd/client/v3
	EtcdClient *etcd.Client

	KvToken       string
	KvAccountId   string
	KvNamespaceId string

	Ctx        context.Context
	HttpClient = &http.Client{}
	YtdlCl     youtubedl.Client
	YtSvc      *youtube.Service

	YtKey             string
	YtMaxResults      int64 = 50
	YtUsername        string
	YtChannelId       string
	YtPlaylistId      string
	YtLastPublishedAt string

	TgApiUrlBase string = "https://api.telegram.org"

	TgToken        string
	TgChatId       string
	TgBossChatId   string
	TgPerformer    string
	TgTitleCleanRe string
	TgTitleUnquote bool

	TgVideosInterval time.Duration

	TgAudioBitrateKbps int64 = 60

	FfmpegPath string = "/bin/ffmpeg"
)

func init() {
	var err error

	if os.Getenv("YamlConfigPath") != "" {
		YamlConfigPath = os.Getenv("YamlConfigPath")
	}
	if YamlConfigPath == "" {
		log("WARNING YamlConfigPath empty")
	}

	EtcdEndpoint, err = GetVar("EtcdEndpoint")
	if err != nil {
		log("ERROR GetVar EtcdEndpoint: %v", err)
		os.Exit(1)
	} else if EtcdEndpoint == "" {
		log("WARNING EtcdEndpoint empty")
	}
	log("DEBUG EtcdEndpoint:`%s`", EtcdEndpoint)

	EtcdRootPassword, err = GetVar("EtcdRootPassword")
	if err != nil {
		log("ERROR GetVar EtcdRootPassword: %v", err)
		os.Exit(1)
	} else if EtcdRootPassword == "" {
		log("WARNING EtcdRootPassword empty")
	}
	log("DEBUG EtcdRootPassword:`%s`", EtcdRootPassword)

	EtcdKeyPrefix, err = GetVar("EtcdKeyPrefix")
	if err != nil {
		log("ERROR GetVar EtcdKeyPrefix: %v", err)
		os.Exit(1)
	} else if EtcdKeyPrefix == "" {
		log("WARNING EtcdKeyPrefix empty")
	}
	log("DEBUG EtcdKeyPrefix:`%s`", EtcdKeyPrefix)

	if EtcdEndpoint != "" && EtcdRootPassword != "" && EtcdKeyPrefix != "" {
		// https://pkg.go.dev/go.etcd.io/etcd/client/v3#Config
		EtcdClient, err = etcd.New(etcd.Config{
			Endpoints:   []string{EtcdEndpoint},
			Username:    "root",
			Password:    EtcdRootPassword,
			DialTimeout: 3 * time.Second,
			TLS:         &tls.Config{InsecureSkipVerify: true},
		})
		if err != nil {
			log("ERROR etcd.New: %v", err)
			os.Exit(1)
		}
	}

	KvToken, err = GetVar("KvToken")
	if err != nil {
		tglog("ERROR %s", err)
		os.Exit(1)
	}
	if KvToken == "" {
		log("WARNING KvToken empty")
	}

	KvAccountId, err = GetVar("KvAccountId")
	if err != nil {
		tglog("ERROR %s", err)
		os.Exit(1)
	}
	if KvAccountId == "" {
		log("WARNING KvAccountId empty")
	}

	KvNamespaceId, err = GetVar("KvNamespaceId")
	if err != nil {
		tglog("ERROR %s", err)
		os.Exit(1)
	}
	if KvNamespaceId == "" {
		log("WARNING KvNamespaceId empty")
	}

	if s, err := GetVar("DEBUG"); err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	} else if s != "" {
		DEBUG = true
		log("DEBUG: true")
	}

	Ctx = context.TODO()
	YtdlCl = youtubedl.Client{HTTPClient: &http.Client{}}

	if s, _ := GetVar("Interval"); s != "" {
		Interval, err = time.ParseDuration(s)
		if err != nil {
			log("ERROR time.ParseDuration Interval:`%s`: %v", s, err)
			os.Exit(1)
		}
		log("Interval: %v", Interval)
	} else {
		log("ERROR Interval empty")
		os.Exit(1)
	}

	if v, _ := GetVar("TgApiUrlBase"); v != "" {
		TgApiUrlBase = v
		log("TgApiUrlBase: `%s`", TgApiUrlBase)
	}

	TgToken, err = GetVar("TgToken")
	if err != nil {
		tglog("ERROR %v", err)
		os.Exit(1)
	}
	if TgToken == "" {
		log("ERROR TgToken empty")
		os.Exit(1)
	}

	TgBossChatId, err = GetVar("TgBossChatId")
	if err != nil {
		tglog("ERROR %v", err)
		os.Exit(1)
	}
	if TgBossChatId == "" {
		log("ERROR TgBossChatId empty")
		os.Exit(1)
	}

	TgChatId, err = GetVar("TgChatId")
	if err != nil {
		tglog("ERROR %v", err)
		os.Exit(1)
	}
	if TgChatId == "" {
		tglog("ERROR TgChatId empty")
		os.Exit(1)
	}

	TgPerformer, err = GetVar("TgPerformer")
	if err != nil {
		tglog("ERROR %v", err)
		os.Exit(1)
	}

	if s, _ := GetVar("TgAudioBitrateKbps"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			TgAudioBitrateKbps = v
			log("TgAudioBitrateKbps: %v", TgAudioBitrateKbps)
		} else {
			log("WARNING invalid TgAudioBitrateKbps:`%s`", s)
		}
	}

	TgTitleCleanRe, err = GetVar("TgTitleCleanRe")
	if err != nil {
		tglog("ERROR %v", err)
		os.Exit(1)
	}

	if v, err := GetVar("TgTitleUnquote"); err != nil {
		tglog("ERROR %v", err)
		os.Exit(1)
	} else if v != "" {
		TgTitleUnquote = true
	}

	if s, _ := GetVar("TgVideosInterval"); s != "" {
		TgVideosInterval, err = time.ParseDuration(s)
		if err != nil {
			log("ERROR time.ParseDuration TgVideosInterval:`%s`: %v", s, err)
			os.Exit(1)
		}
		log("TgVideosInterval: %v", TgVideosInterval)
	} else {
		log("ERROR TgVideosInterval empty")
		os.Exit(1)
	}

	YtKey, err = GetVar("YtKey")
	if err != nil {
		tglog("ERROR %v", err)
		os.Exit(1)
	}
	if YtKey == "" {
		tglog("ERROR YtKey empty")
		os.Exit(1)
	}

	if v, err := GetVar("YtMaxResults"); err != nil {
		tglog("ERROR %v", err)
		os.Exit(1)
	} else if v != "" {
		YtMaxResults, err = strconv.ParseInt(v, 10, 0)
		if err != nil {
			log("ERROR invalid YtMaxResults: %v", err)
			tglog("ERROR invalid YtMaxResults: %v", err)
			os.Exit(1)
		}
	}

	YtUsername, err = GetVar("YtUsername")
	if err != nil {
		tglog("ERROR %v", err)
		os.Exit(1)
	}

	YtChannelId, err = GetVar("YtChannelId")
	if err != nil {
		tglog("ERROR %v", err)
		os.Exit(1)
	}

	YtPlaylistId, err = GetVar("YtPlaylistId")
	if err != nil {
		tglog("ERROR %v", err)
		os.Exit(1)
	}

	YtLastPublishedAt, err = GetVar("YtLastPublishedAt")
	if err != nil {
		tglog("ERROR %v", err)
		os.Exit(1)
	}

	if v, err := GetVar("FfmpegPath"); err != nil {
		tglog("ERROR %v", err)
		os.Exit(1)
	} else {
		FfmpegPath = v
	}
	log("FfmpegPath: `%s`", FfmpegPath)
}

func main() {
	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM)
	go func(sigterm chan os.Signal) {
		<-sigterm
		tglog("%s: sigterm", os.Args[0])
		os.Exit(1)
	}(sigterm)

	if EtcdClient != nil {
		defer EtcdClient.Close()
	}

	for {
		t0 := time.Now()

		processYtChannel()

		if dur := time.Now().Sub(t0); dur < Interval {
			time.Sleep(Interval - dur)
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

	YtSvc, err = youtube.NewService(Ctx, youtubeoption.WithAPIKey(YtKey))
	if err != nil {
		tglog("ERROR youtube NewService: %s", err)
		os.Exit(1)
	}

	if YtPlaylistId == "" {
		if YtUsername == "" && YtChannelId == "" {
			tglog("Empty YtUsername and YtChannelId, nothing to do")
			os.Exit(1)
		}

		// https://developers.google.com/youtube/v3/docs/channels/list

		channelslistcall := YtSvc.Channels.List([]string{"id", "snippet", "contentDetails"}).MaxResults(11)
		if YtChannelId != "" {
			channelslistcall = channelslistcall.Id(YtChannelId)
		} else if YtUsername != "" {
			channelslistcall = channelslistcall.ForUsername(YtUsername)
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
		YtPlaylistId = channelslist.Items[0].ContentDetails.RelatedPlaylists.Uploads
	}

	// https://developers.google.com/youtube/v3/docs/playlistItems/list

	var videos []youtube.PlaylistItemSnippet

	playlistitemslistcall := YtSvc.PlaylistItems.List([]string{"id", "snippet", "contentDetails"}).MaxResults(YtMaxResults)
	playlistitemslistcall = playlistitemslistcall.PlaylistId(YtPlaylistId)
	err = playlistitemslistcall.Pages(
		Ctx,
		func(r *youtube.PlaylistItemListResponse) error {
			for _, i := range r.Items {
				if YtLastPublishedAt == "" || i.Snippet.PublishedAt > YtLastPublishedAt {
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
		if TgTitleCleanRe != "" {
			vtitle = regexp.MustCompile(TgTitleCleanRe).ReplaceAllString(vtitle, "")
		}
		if TgTitleUnquote {
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

		var audioFormat youtubedl.Format
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

		if FfmpegPath != "" && TgAudioBitrateKbps > 0 {
			log("DEBUG target audio bitrate:%dkbps", TgAudioBitrateKbps)
			audioFile = fmt.Sprintf("%s..%dk..m4a", audioName, TgAudioBitrateKbps)
			err = exec.Command(
				FfmpegPath, "-v", "panic",
				"-i", audioSrcFile,
				"-b:a", fmt.Sprintf("%dk", TgAudioBitrateKbps),
				audioFile,
			).Run()
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
			TgPerformer,
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
			fmt.Sprintf("%s "+NL+"%s "+NL+"youtu.be/%s %s ", vtitle, TgPerformer, v.ResourceId.VideoId, vinfo.Duration),
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

		YtLastPublishedAt = vpatime.Format(time.RFC3339)
		err = SetVar("YtLastPublishedAt", YtLastPublishedAt)
		if err != nil {
			tglog("ERROR SetVar YtLastPublishedAt: %v", err)
			break
		}

		if len(videos) > 10 {
			log("sleeping %v", TgVideosInterval)
			time.Sleep(TgVideosInterval)
		}
	}

	return
}

func tglog(msg interface{}, args ...interface{}) error {
	log(msg, args...)
	msgtext := fmt.Sprintf(fmt.Sprintf("%s", msg), args...) + NL

	smreq := TgSendMessageRequest{
		ChatId:                TgBossChatId,
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
	tgapiurl := fmt.Sprintf("%s/bot%s/sendMessage", TgApiUrlBase, TgToken)
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
		ChatId:                TgChatId,
		Text:                  message,
		DisableWebPagePreview: true,
	}
	sendMessageJSON, err := json.Marshal(sendMessage)
	if err != nil {
		return nil, err
	}

	var tgresp TgResponse
	err = postJson(
		fmt.Sprintf("%s/bot%s/sendMessage", TgApiUrlBase, TgToken),
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
		"chat_id":    TgChatId,
		"message_id": messageid,
	}
	deleteMessageJSON, err := json.Marshal(deleteMessage)
	if err != nil {
		return err
	}

	var tgresp TgResponseShort
	err = postJson(
		fmt.Sprintf("%s/bot%s/deleteMessage", TgApiUrlBase, TgToken),
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
	_, err = formWr.Write([]byte(TgChatId))
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
		fmt.Sprintf("%s/bot%s/sendAudio", TgApiUrlBase, TgToken),
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
		"chat_id": TgChatId,
		"audio":   fileid,
		"caption": caption,
	}
	sendAudioJSON, err := json.Marshal(sendAudio)
	if err != nil {
		return nil, err
	}

	var tgresp TgResponse
	err = postJson(
		fmt.Sprintf("%s/bot%s/sendAudio", TgApiUrlBase, TgToken),
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
	_, err = formWr.Write([]byte(TgChatId))
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
		fmt.Sprintf("%s/bot%s/sendPhoto", TgApiUrlBase, TgToken),
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
		"chat_id":    TgChatId,
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
		fmt.Sprintf("%s/bot%s/sendPhoto", TgApiUrlBase, TgToken),
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

func GetVar(name string) (value string, err error) {
	value = os.Getenv(name)

	if YamlConfigPath != "" {
		if v, err := YamlGet(name); err != nil {
			log("WARNING GetVar YamlGet `%s`: %v", name, err)
			return "", err
		} else if v != "" {
			value = v
		}
	}

	if EtcdClient != nil {
		if v, err := EtcdGet(name); err != nil {
			log("WARNING GetVar EtcdGet %s: %v", name, err)
			return "", err
		} else if v != "" {
			value = v
		}
	}

	if KvToken != "" && KvAccountId != "" && KvNamespaceId != "" {
		if v, err := KvGet(name); err != nil {
			log("WARNING GetVar KvGet %s: %v", name, err)
			return "", err
		} else if v != "" {
			value = v
		}
	}

	return value, nil
}

func SetVar(name, value string) (err error) {
	if DEBUG {
		log("DEBUG SetVar: %s: %s", name, value)
	}

	if EtcdClient != nil {
		if err := EtcdSet(name, value); err != nil {
			log("WARNING SetVar EtcdSet %s: %v", name, err)
			return err
		}
		return nil
	}

	if KvToken != "" && KvAccountId != "" && KvNamespaceId != "" {
		if err := KvSet(name, value); err != nil {
			log("WARNING SetVar KvSet %s: %v", name, err)
			return err
		}
		return nil
	}

	if YamlConfigPath != "" {
		if err := YamlSet(name, value); err != nil {
			log("WARNING SetVar YamlSet %s: %v", name, err)
			return err
		}
		return nil
	}

	return fmt.Errorf("nor etcd credentials nor kv credentials nor yaml config path provided to save to")
}

func YamlGet(name string) (value string, err error) {
	configf, err := os.Open(YamlConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer configf.Close()

	configm := make(map[interface{}]interface{})
	if err = yaml.NewDecoder(configf).Decode(&configm); err != nil {
		if DEBUG {
			log("WARNING yaml.Decode %s: %v", YamlConfigPath, err)
		}
		return "", err
	}

	if v, ok := configm[name]; ok == true {
		switch v.(type) {
		case string:
			value = v.(string)
		case int:
			value = fmt.Sprintf("%d", v.(int))
		default:
			return "", fmt.Errorf("yaml value of unsupported type, only string and int types are supported")
		}
	}

	return value, nil
}

func YamlSet(name, value string) error {
	configf, err := os.Open(YamlConfigPath)
	if err == nil {
		configm := make(map[interface{}]interface{})
		err := yaml.NewDecoder(configf).Decode(&configm)
		if err != nil {
			log("WARNING yaml.Decode %s: %v", YamlConfigPath, err)
		}
		configf.Close()
		configm[name] = value
		configf, err := os.Create(YamlConfigPath)
		if err == nil {
			defer configf.Close()
			confige := yaml.NewEncoder(configf)
			err := confige.Encode(configm)
			if err == nil {
				confige.Close()
				configf.Close()
			} else {
				log("WARNING yaml.Encoder.Encode: %v", err)
				return err
			}
		} else {
			log("WARNING os.Create config file %s: %v", YamlConfigPath, err)
			return err
		}
	} else {
		log("WARNING os.Open config file %s: %v", YamlConfigPath, err)
		return err
	}

	return nil
}

func EtcdGet(key string) (value string, err error) {
	if resp, err := EtcdClient.Get(context.TODO(), EtcdKeyPrefix+key); err != nil {
		return "", err
	} else if len(resp.Kvs) == 0 {
		return "", nil
	} else {
		value = string(resp.Kvs[0].Value)
	}
	return value, nil
}

func EtcdSet(key, value string) error {
	if _, err := EtcdClient.Put(context.TODO(), EtcdKeyPrefix+key, value); err != nil {
		return err
	}
	return nil
}

func KvGet(name string) (value string, err error) {
	req, err := http.NewRequest(
		"GET",
		fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/storage/kv/namespaces/%s/values/%s", KvAccountId, KvNamespaceId, name),
		nil,
	)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", KvToken))
	resp, err := HttpClient.Do(req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("kv api response status: %s", resp.Status)
	}

	if rbb, err := io.ReadAll(resp.Body); err != nil {
		return "", err
	} else {
		value = string(rbb)
	}

	return value, nil
}

func KvSet(name, value string) error {
	mpbb := new(bytes.Buffer)
	mpw := multipart.NewWriter(mpbb)
	if err := mpw.WriteField("metadata", "{}"); err != nil {
		return err
	}
	if err := mpw.WriteField("value", value); err != nil {
		return err
	}
	mpw.Close()

	req, err := http.NewRequest(
		"PUT",
		fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/storage/kv/namespaces/%s/values/%s", KvAccountId, KvNamespaceId, name),
		mpbb,
	)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", mpw.FormDataContentType())
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", KvToken))
	resp, err := HttpClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("kv api response status: %s", resp.Status)
	}

	return nil
}
