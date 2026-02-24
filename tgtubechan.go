/*

https://github.com/kkdai/youtube/v2/

https://developers.google.com/youtube/v3/docs/playlistItems/list
https://console.cloud.google.com/apis/api/youtube.googleapis.com/quotas

https://core.telegram.org/bots/api


go get github.com/kkdai/youtube/v2@master

GoGet GoFmt GoBuildNull

https://github.com/shoce/tgtubechan/actions

TODO cache playlists

*/

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"image"
	_ "image/jpeg"
	"image/png"

	"golang.org/x/exp/slices"
	_ "golang.org/x/image/webp"

	ytdl "github.com/kkdai/youtube/v2"
	youtubeoption "google.golang.org/api/option"
	youtube "google.golang.org/api/youtube/v3"
	yaml "gopkg.in/yaml.v3"

	"github.com/shoce/tg"
)

const (
	SP = " "
	NL = "\n"

	BEAT = time.Duration(24) * time.Hour / 1000

	TgUpdateLogMaxSizeDefault = 12

	MsgEmbeddingDisabled = "embedding of this video has been disabled"
	MsgLoginRequired     = "login required to confirm your age"
)

type TgTubeChanChannel struct {
	YtUsername   string `yaml:"YtUsername"`
	YtChannelId  string `yaml:"YtChannelId"`
	YtPlaylistId string `yaml:"YtPlaylistId"`
	YtLast       string `yaml:"YtLast"`

	TgChatId          string `yaml:"TgChatId"`
	TgPerformer       string `yaml:"TgPerformer"`
	TgTitleCleanRe    string `yaml:"TgTitleCleanRe"`
	TgTitleUnquote    bool   `yaml:"TgTitleUnquote"`
	TgSkipPhoto       bool   `yaml:"TgSkipPhoto"`
	TgSkipDescription bool   `yaml:"TgSkipDescription"`

	Suspend bool `yaml:"Suspend"`
}

type TgTubeChanConfig struct {
	YssUrl string `yaml:"-"`

	DEBUG bool `yaml:"DEBUG"`

	Interval time.Duration `yaml:"Interval"`

	TgApiUrl     string `yaml:"TgApiUrl"`     // "https://api.telegram.org" "http://tgbotserver:80"
	TgApiUrlBase string `yaml:"TgApiUrlBase"` // "https://api.telegram.org" "http://tgbotserver:80"

	TgToken  string `yaml:"TgToken"`
	TgChatId string `yaml:"TgChatId"`
	TgBossId string `yaml:"TgBossId"`

	TgUpdateLog        []int64 `yaml:"TgUpdateLog,flow"`
	TgUpdateLogMaxSize int     `yaml:"TgUpdateLogMaxSize"` // = 12

	TgPlaylistVideosInterval time.Duration `yaml:"TgPlaylistVideosInterval"`

	TgAudioBitrateKbps int64 `yaml:"TgAudioBitrateKbps"` // 60

	DssUrl string `yaml:"DssUrl"` // "http://dss:80"

	YtKey        string `yaml:"YtKey"`
	YtMaxResults int64  `yaml:"YtMaxResults"` // 50
	YtThrottle   int64  `yaml:"YtThrottle"`   // 12

	YtUserAgent string `yaml:"YtUserAgent"` // "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.6 Safari/605.1.15"

	FfmpegPath          string   `yaml:"FfmpegPath"`          // "/bin/ffmpeg"
	FfmpegGlobalOptions []string `yaml:"FfmpegGlobalOptions"` // []string{"-v", "panic"}

	Channels []TgTubeChanChannel `yaml:"Channels"`
}

var (
	Config TgTubeChanConfig

	TZIST = time.FixedZone("IST", 330*60)

	Ctx context.Context

	HttpClient = &http.Client{}

	YtdlCl ytdl.Client
	YtSvc  *youtube.Service

	ENUFF = errors.New("ENUFF")

	TgTitleCleanRe *regexp.Regexp

	YtChannelReText = `youtube\.com/@([-_A-Za-z0-9]+)`
	YtChannelRe     *regexp.Regexp

	BTOI = map[bool]int{false: 0, true: 1}
)

func init() {

	Ctx = context.TODO()

	// https://pkg.go.dev/regexp
	YtChannelRe = regexp.MustCompile(YtChannelReText)

	if v := os.Getenv("YssUrl"); v != "" {
		Config.YssUrl = v
	}
	if Config.YssUrl == "" {
		perr("ERROR YssUrl empty")
		os.Exit(1)
	}

	if err := ConfigGet(); err != nil {
		perr("ERROR ConfigGet %v", err)
		os.Exit(1)
	}

	ytdl.VisitorIdMaxAge = 33 * time.Minute

	/*
		// https://pkg.go.dev/github.com/kkdai/youtube/v2/#pkg-variables
		ytdl.IOSClient = ytdl.ClientInfo{
			Name:        "IOS",
			Version:     "19.49.7",
			Key:         "AIzaSyAO_FJ2SlqU8Q4STEHLGCilw_Y9_11qcW8",
			UserAgent:   "com.google.ios.youtube/19.49.7 (iPhone16,2; U; CPU iOS 17_5_1 like Mac OS X;)",
			DeviceModel: "iPhone16,2",
		}
	*/
	// WebClient AndroidClient IOSClient EmbeddedClient
	ytdl.DefaultClient = ytdl.IOSClient

	rand.Seed(time.Now().UnixNano())

}

func ConfigGet() (err error) {

	if err = Config.Get(); err != nil {
		return err
	}

	if Config.DEBUG {
		perr("DEBUG <true>")
		tg.DEBUG = true
	}

	perr("Interval <%v>", Config.Interval)
	if Config.Interval == 0 {
		perr("ERROR Interval empty")
		os.Exit(1)
	}

	perr("TgApiUrl [%s]", Config.TgApiUrl)
	perr("TgApiUrlBase [%s]", Config.TgApiUrlBase)
	if Config.TgApiUrl == "" {
		perr("ERROR TgApiUrl empty")
		if Config.TgApiUrlBase != "" {
			Config.TgApiUrl = Config.TgApiUrlBase
			perr("TgApiUrl [%s]", Config.TgApiUrl)
		} else {
			os.Exit(1)
		}
	}

	tg.ApiUrl = Config.TgApiUrl

	if Config.TgToken == "" {
		return fmt.Errorf("TgToken empty")
	}

	tg.ApiToken = Config.TgToken

	if Config.TgChatId == "" {
		return fmt.Errorf("TgChatId empty")
	}

	if Config.TgBossId == "" {
		return fmt.Errorf("TgBossId empty")
	}

	perr("TgUpdateLog %v", Config.TgUpdateLog)
	if Config.TgUpdateLogMaxSize <= 0 {
		Config.TgUpdateLogMaxSize = TgUpdateLogMaxSizeDefault
	}
	perr("TgUpdateLogMaxSize <%d>", Config.TgUpdateLogMaxSize)

	if Config.TgPlaylistVideosInterval == 0 {
		return fmt.Errorf("TgPlaylistVideosInterval empty")
	}

	if Config.TgAudioBitrateKbps == 0 {
		return fmt.Errorf("TgAudioBitrateKbps empty")
	}

	perr("DssUrl [%s]", Config.DssUrl)

	for i, channel := range Config.Channels {
		if channel.YtUsername == "" {
			return fmt.Errorf("Channel <%d> YtUsername empty", i)
		}
		if channel.TgTitleCleanRe != "" {
			if _, err := regexp.Compile(channel.TgTitleCleanRe); err != nil {
				return fmt.Errorf("Channel [%s] TgTitleCleanRe [%s] %v", channel.YtUsername, channel.TgTitleCleanRe, err)
			}
		}
	}

	if Config.YtKey == "" {
		return fmt.Errorf("YtKey empty")
	}

	if YtSvc, err = youtube.NewService(Ctx, youtubeoption.WithAPIKey(Config.YtKey)); err != nil {
		return fmt.Errorf("youtube NewService %v", err)
	}

	if Config.YtMaxResults == 0 {
		Config.YtMaxResults = 50
	}

	if Config.YtThrottle == 0 {
		Config.YtThrottle = 12
	}
	perr("YtThrottle <%d>", Config.YtThrottle)

	perr("FfmpegPath [%s]", Config.FfmpegPath)
	perr("FfmpegGlobalOptions (%+v)", Config.FfmpegGlobalOptions)

	perr("Channels (")
	for _, channel := range Config.Channels {
		perr("{ @Suspend <%v> @YtUsername [%s] @YtLast <%s> }", BTOI[channel.Suspend], channel.YtUsername, channel.YtLast)
	}
	perr(")")

	return nil

}

func main() {
	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM)
	go func(sigterm chan os.Signal) {
		<-sigterm
		tglog("%s sigterm", os.Args[0])
		os.Exit(1)
	}(sigterm)

	for {
		err := ConfigGet()
		if err != nil {
			perr("ERROR ConfigGet %v", err)
			os.Exit(1)
		}

		ticker := time.NewTicker(Config.Interval)

		err = TgGetUpdates()
		if err != nil {
			perr("ERROR TgGetUpdates %v", err)
		}

		channels := Config.Channels
		rand.Shuffle(len(channels), func(i, j int) {
			channels[i], channels[j] = channels[j], channels[i]
		})

		for jchannel, _ := range channels {
			channel := &channels[jchannel]
			if channel.Suspend {
				perr("DEBUG %s suspended", channel.YtUsername)
				continue
			} else {
				perr("DEBUG %s", channel.YtUsername)
			}
			if err := processYtChannel(channel); err != nil {
				tglog("ERROR %s %v", channel.YtUsername, err)
			}
		}

		perr("DEBUG sleeping")
		<-ticker.C
	}

	return
}

func TgGetUpdates() (err error) {

	var updatesoffset int64
	if len(Config.TgUpdateLog) > 0 {
		updatesoffset = Config.TgUpdateLog[len(Config.TgUpdateLog)-1] + 1
	}

	var uu []tg.Update
	uu, _, err = tg.GetUpdates(updatesoffset)
	if err != nil {
		return fmt.Errorf("tg.GetUpdates %v", err)
	}

	for _, u := range uu {
		perr("Update %s", strings.ReplaceAll(tg.F("%+v", u), NL, "<NL>"))
		if slices.Contains(Config.TgUpdateLog, u.UpdateId) {
			perr("WARNING this telegram update id <%d> was already processed, skipping", u.UpdateId)
			continue
		}
		Config.TgUpdateLog = append(Config.TgUpdateLog, u.UpdateId)
		if len(Config.TgUpdateLog) > Config.TgUpdateLogMaxSize {
			Config.TgUpdateLog = Config.TgUpdateLog[len(Config.TgUpdateLog)-Config.TgUpdateLogMaxSize:]
		}
		if err := Config.Put(); err != nil {
			return fmt.Errorf("Config.Put %v", err)
		}

		if u.MyChatMember.Date != 0 {
			cm := u.MyChatMember
			if tg.F("%d", cm.From.Id) != Config.TgBossId {
				continue
			}
			if cm.Chat.Type != "channel" {
				continue
			}
			// TODO check User.Id equal to the bot's user id
			//if cm.NewChatMember.User.Id !=
			if cm.NewChatMember.Status != "administrator" {
				continue
			}

			var tgmsg string

			chatfullinfo, err := tg.GetChat(cm.Chat.Id)
			if err != nil {
				perr("ERROR tg.GetChat <%d> %v", cm.Chat.Id, err)
				tgmsg = tg.Bold(cm.Chat.Title) + NL + NL + tg.Italic("no description")
			} else {
				tgmsg = tg.Bold(strings.ToUpper(chatfullinfo.Title)) + NL + NL + tg.Esc(chatfullinfo.Description)
			}

			if _, err := tg.SendMessage(tg.SendMessageRequest{
				ChatId: fmt.Sprintf("%d", cm.Chat.Id),
				Text:   tgmsg,
			}); err != nil {
				perr("tg.SendMessage %v", err)
				return err
			}

			if YtChannelRe.MatchString(chatfullinfo.Description) {
				// https://pkg.go.dev/regexp#Regexp.FindStringSubmatch
				ssm := YtChannelRe.FindStringSubmatch(chatfullinfo.Description)
				if ssm == nil || len(ssm) != 2 {
					perr("ERROR YtChannelRe regexp is broken")
					return fmt.Errorf("YtChannelRe regexp is broken FindStringSubmatch %#v", ssm)
				}
				newchannel := TgTubeChanChannel{
					YtUsername: ssm[1],
					TgChatId:   tg.F("%d", chatfullinfo.Id),
					//TgPerformer:       chatfullinfo.Title,
					TgSkipPhoto:       false,
					TgSkipDescription: false,
				}
				perr("DEBUG new channel %#v", newchannel)
				if _, err := tg.SendMessage(tg.SendMessageRequest{
					ChatId: fmt.Sprintf("%d", cm.Chat.Id),
					Text:   tg.Esc(tg.F("new channel %#v", newchannel)),
				}); err != nil {
					perr("tg.SendMessage %v", err)
					return err
				}

				addchannel := true
				for _, c := range Config.Channels {
					if c.YtUsername == newchannel.YtUsername {
						addchannel = false
						break
					}
				}

				if addchannel {
					Config.Channels = append(Config.Channels, newchannel)
					if err := Config.Put(); err != nil {
						return fmt.Errorf("Config.Put %v", err)
					}
				}

			}

		}
	}

	return nil

}

func processYtChannel(channel *TgTubeChanChannel) (err error) {
	if channel.YtPlaylistId == "" {
		if channel.YtUsername == "" && channel.YtChannelId == "" {
			return fmt.Errorf("both YtUsername and YtChannelId empty")
		}

		// https://developers.google.com/youtube/v3/docs/channels/list

		channelslistcall := YtSvc.Channels.List([]string{"id", "snippet", "contentDetails"}).MaxResults(11)
		if channel.YtChannelId != "" {
			channelslistcall = channelslistcall.Id(channel.YtChannelId)
		} else if channel.YtUsername != "" {
			// https://developers.google.com/youtube/v3/docs/channels/list#forHandle
			channelslistcall = channelslistcall.ForHandle(channel.YtUsername)
		}
		channelslist, err := channelslistcall.Do()
		if err != nil {
			return fmt.Errorf("channels/list %v", err)
		}

		if len(channelslist.Items) == 0 {
			return fmt.Errorf("channels/list empty result")
		}

		for _, c := range channelslist.Items {
			tglog(
				"DEBUG %s channel id [%s] title [%s] uploads playlist id <%v>",
				channel.YtUsername, c.Id, c.Snippet.Title, c.ContentDetails.RelatedPlaylists.Uploads,
			)
		}
		if len(channelslist.Items) > 1 {
			return fmt.Errorf("channels/list more than one result")
		}

		if channel.YtChannelId == "" {
			channel.YtChannelId = channelslist.Items[0].Id
		}
		channel.YtPlaylistId = channelslist.Items[0].ContentDetails.RelatedPlaylists.Uploads
		if err := Config.Put(); err != nil {
			return fmt.Errorf("Config.Put %v", err)
		}
	}

	var videos []youtube.PlaylistItemSnippet

	// https://developers.google.com/youtube/v3/docs/playlistItems/list
	playlistitemslistcall := YtSvc.PlaylistItems.List([]string{"id", "snippet", "contentDetails"}).MaxResults(Config.YtMaxResults)
	playlistitemslistcall = playlistitemslistcall.PlaylistId(channel.YtPlaylistId)
	// TODO request results sorted by date asc
	if err = playlistitemslistcall.Pages(
		Ctx,
		func(resp *youtube.PlaylistItemListResponse) error {
			for _, item := range resp.Items {
				//log("DEBUG %s playlistitem %02d %s", channel.YtUsername, jitem+1, item.Snippet.PublishedAt)
				// item.Snippet.PublishedAt channel.YtLast are strings
				// playlistitems/list results are sorted by date desc
				// stop Pages after receiving an item older than YtLast
				if channel.YtLast != "" && item.Snippet.PublishedAt <= channel.YtLast {
					return ENUFF
				}
				videos = append(videos, *item.Snippet)
			}
			return nil
		},
	); err != nil && err != ENUFF {
		return fmt.Errorf("playlistitems/list %v", err)
	}

	sort.Slice(videos, func(i, j int) bool { return videos[i].PublishedAt < videos[j].PublishedAt })

	YtdlCl = ytdl.Client{
		HTTPClient: &http.Client{
			Transport: &UserAgentTransport{
				http.DefaultTransport,
				Config.YtUserAgent,
			},
		},
	}

	for j, v := range videos {
		perr(
			"DEBUG %s <%d>/<%d> title [%s] url [youtu.be/%s] published <%s>",
			channel.YtUsername, j+1, len(videos), v.Title, v.ResourceId.VideoId, v.PublishedAt,
		)

		vpatime, err := time.Parse(time.RFC3339, v.PublishedAt)
		if err != nil {
			return fmt.Errorf("time.Parse PublishedAt [%s] %v", v.PublishedAt, err)
		}

		vinfo, err := YtdlCl.GetVideoContext(Ctx, v.ResourceId.VideoId)
		if err != nil {

			if _, ok := err.(*ytdl.ErrPlayabiltyStatus); ok {

				// cannot playback and download, status: LIVE_STREAM_OFFLINE, reason: This live event will begin in a few moments.
				if err.(*ytdl.ErrPlayabiltyStatus).Status == "LIVE_STREAM_OFFLINE" && time.Now().Sub(vpatime) > 24*time.Hour {
					perr("DEBUG GetVideoContext skipping LIVE_STREAM_OFFLINE youtu.be/%s", v.ResourceId.VideoId)
					continue
				}

				// youtu.be/6L37mxTMxcQ Video unavailable. This video contains content from Beggars Group Digital, who has blocked it in your country on copyright grounds.
				if err.(*ytdl.ErrPlayabiltyStatus).Status == "UNPLAYABLE" {
					tgmsg := tg.Italic("UNPLAYABLE") + NL +
						tg.Esc(tg.F(
							"%s"+NL+"%s %s"+NL+"youtu.be/%s",
							v.Title, channel.TgPerformer, vpatime.Format("2006/01/02"), v.ResourceId.VideoId,
						))
					if _, tgerr := tg.SendMessage(tg.SendMessageRequest{
						ChatId: channel.TgChatId,
						Text:   tgmsg,

						LinkPreviewOptions: tg.LinkPreviewOptions{IsDisabled: true},
					}); tgerr != nil {
						return fmt.Errorf("tg.SendMessage %v", tgerr)
					}

					channel.YtLast = vpatime.Format(time.RFC3339)
					if err := Config.Put(); err != nil {
						perr("Config.Put %v", err)
					}

					continue
				}

			}

			// can't bypass age restriction: embedding of this video has been disabled
			if err2 := errors.Unwrap(err); err2 != nil && err2.Error() == MsgEmbeddingDisabled {
				tgmsg := tg.Italic(MsgEmbeddingDisabled) + NL +
					tg.Esc(tg.F(
						"%s"+NL+"%s %s"+NL+"youtu.be/%s",
						v.Title, channel.TgPerformer, vpatime.Format("2006/01/02"), v.ResourceId.VideoId,
					))
				if _, tgerr := tg.SendMessage(tg.SendMessageRequest{
					ChatId: channel.TgChatId,
					Text:   tgmsg,

					LinkPreviewOptions: tg.LinkPreviewOptions{IsDisabled: true},
				}); tgerr != nil {
					return fmt.Errorf("tg.SendMessage %v", tgerr)
				}

				channel.YtLast = vpatime.Format(time.RFC3339)
				if err := Config.Put(); err != nil {
					perr("Config.Put %v", err)
				}

				continue
			}

			// login required to confirm your age
			if err.Error() == MsgLoginRequired {
				tgmsg := tg.Italic(MsgLoginRequired) + NL +
					tg.Esc(tg.F(
						"%s"+NL+"%s %s"+NL+"youtu.be/%s",
						v.Title, channel.TgPerformer, vpatime.Format("2006/01/02"), v.ResourceId.VideoId,
					))
				if _, tgerr := tg.SendMessage(tg.SendMessageRequest{
					ChatId: channel.TgChatId,
					Text:   tgmsg,

					LinkPreviewOptions: tg.LinkPreviewOptions{IsDisabled: true},
				}); tgerr != nil {
					return fmt.Errorf("tg.SendMessage %v", tgerr)
				}

				channel.YtLast = vpatime.Format(time.RFC3339)
				if err := Config.Put(); err != nil {
					perr("Config.Put %v", err)
				}

				continue
			}

			return fmt.Errorf("GetVideoContext %#v"+NL+"%#v", err, v)

		}

		vtitle := v.Title

		if TgTitleCleanRe != nil {
			vtitle = TgTitleCleanRe.ReplaceAllString(vtitle, "")
		}

		if channel.TgTitleUnquote {
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

		audioName := fmt.Sprintf(
			"%04d%02d%02d.%02d%02d%02d.%s",
			vpatime.Year(), vpatime.Month(), vpatime.Day(),
			vpatime.Hour(), vpatime.Minute(), vpatime.Second(),
			v.ResourceId.VideoId,
		)

		var audioFormat ytdl.Format
		for _, f := range vinfo.Formats.WithAudioChannels() {
			if !strings.HasPrefix(f.MimeType, "audio/mp4") {
				continue
			}
			perr("DEBUG format size <%dmb> AudioTrack %+v", f.ContentLength>>20, f.AudioTrack)
			if f.AudioTrack != nil && !strings.HasSuffix(f.AudioTrack.DisplayName, " original") {
				continue
			}
			if audioFormat.Bitrate == 0 || f.Bitrate > audioFormat.Bitrate {
				perr("DEBUG pick")
				audioFormat = f
			}
		}

		var ytstream io.ReadCloser
		if ytstream, _, err = YtdlCl.GetStreamContext(Ctx, vinfo, &audioFormat); err != nil {
			return fmt.Errorf("GetStreamContext %v", err)
		}
		defer ytstream.Close()

		ytstreamthrottled := &ThrottledReader{Reader: ytstream, Bps: int64(audioFormat.Bitrate) * Config.YtThrottle}

		// https://pkg.go.dev/bytes#Buffer
		var audioBuf *bytes.Buffer
		audioBuf = bytes.NewBuffer(nil)

		t0dl := time.Now()
		if _, err = io.Copy(audioBuf, ytstreamthrottled); err != nil {
			return fmt.Errorf("copy stream %v", err)
		}

		perr("DEBUG downloaded audio size <%dmb> bitrate <%dkbps> duration <%v> in <%v>",
			audioBuf.Len()>>20, audioFormat.Bitrate>>10, vinfo.Duration, time.Now().Sub(t0dl).Truncate(time.Second),
		)

		if expectsize := int(vinfo.Duration.Seconds()) * audioFormat.Bitrate / 8; audioBuf.Len() < expectsize/2 {
			return fmt.Errorf("downloaded audio size is less than half of expected")
		}

		audioSrcFile := fmt.Sprintf("%s..m4a", audioName)
		if err = ioutil.WriteFile(audioSrcFile, audioBuf.Bytes(), 0400); err != nil {
			return fmt.Errorf("WriteFile %s %v", audioSrcFile, err)
		}

		audioFile := audioSrcFile

		if Config.FfmpegPath != "" && Config.TgAudioBitrateKbps > 0 {
			perr("DEBUG target audio bitrate <%dkbps>", Config.TgAudioBitrateKbps)
			audioFile = fmt.Sprintf("%s..%dk..m4a", audioName, Config.TgAudioBitrateKbps)
			ffmpegArgs := append(Config.FfmpegGlobalOptions,
				"-i", audioSrcFile,
				"-b:a", fmt.Sprintf("%dk", Config.TgAudioBitrateKbps),
				audioFile,
			)
			if err = exec.Command(Config.FfmpegPath, ffmpegArgs...).Run(); err != nil {
				return fmt.Errorf("ffmpeg (%s %v) %v", Config.FfmpegPath, ffmpegArgs, err)
			}

			if err = os.Remove(audioSrcFile); err != nil {
				tglog("ERROR Remove %s %v", audioSrcFile, err)
			}
		}

		audioBytes, err := ioutil.ReadFile(audioFile)
		if err != nil {
			return fmt.Errorf("ReadFile %s %v", audioFile, err)
		} else if err := os.Remove(audioFile); err != nil {
			tglog("ERROR os.Remove %s %v", audioFile, err)
		}

		var thumbUrl string
		if v.Thumbnails.Maxres != nil && v.Thumbnails.Maxres.Url != "" {
			thumbUrl = v.Thumbnails.Maxres.Url
		} else if v.Thumbnails.Standard != nil && v.Thumbnails.Standard.Url != "" {
			thumbUrl = v.Thumbnails.Standard.Url
		} else if v.Thumbnails.High != nil && v.Thumbnails.High.Url != "" {
			thumbUrl = v.Thumbnails.High.Url
		} else if v.Thumbnails.Medium != nil && v.Thumbnails.Medium.Url != "" {
			thumbUrl = v.Thumbnails.Medium.Url
		} else {
			return fmt.Errorf("no thumb url")
		}

		var thumbBytes []byte
		thumbBytes, err = downloadFile(thumbUrl)
		if err != nil {
			return fmt.Errorf("download thumb url [%s] %v", thumbUrl, err)
		}

		if thumbImg, thumbImgFmt, err := image.Decode(bytes.NewReader(thumbBytes)); err != nil {
			perr("ERROR thumb url [%s] decode %v", thumbUrl, err)
		} else {
			dx, dy := thumbImg.Bounds().Dx(), thumbImg.Bounds().Dy()
			perr("DEBUG thumb url [%s] fmt [%s] size <%dkb> res <%dx%d>", thumbUrl, thumbImgFmt, len(thumbBytes)>>10, dx, dy)
			if thumbImgFmt == "webp" {
				thumbPngBuf := new(bytes.Buffer)
				png.Encode(thumbPngBuf, thumbImg)
				thumbBytes = thumbPngBuf.Bytes()
				perr("DEBUG thumb url [%s] converted to fmt [png] size <%dkb>", thumbUrl, len(thumbBytes)>>10)
			}
		}

		if !channel.TgSkipPhoto {

			var tgcover tg.PhotoSize
			if tgmsg, err := tg.SendPhotoFile(tg.SendPhotoFileRequest{
				ChatId:   channel.TgChatId,
				FileName: audioName + "..photo",
				Photo:    bytes.NewReader(thumbBytes),
			}); err != nil {
				return fmt.Errorf("ERROR tg.SendPhotoFile %v", err)
			} else {
				for _, p := range tgmsg.Photo {
					if p.Width > tgcover.Width {
						tgcover = p
					}
				}
				if tgcover.FileId == "" {
					return fmt.Errorf("ERROR tg.SendPhotoFile file_id empty")
				}
				if err := tg.DeleteMessage(tg.DeleteMessageRequest{
					ChatId:    channel.TgChatId,
					MessageId: tgmsg.MessageId,
				}); err != nil {
					perr("ERROR tg.DeleteMessage: %v", err)
				}
			}

			photoCaption := tg.BoldUnderline(tg.Esc(vtitle))

			if _, err := tg.SendPhoto(tg.SendPhotoRequest{
				ChatId:  channel.TgChatId,
				Photo:   tgcover.FileId,
				Caption: photoCaption,
			}); err != nil {
				return fmt.Errorf("tg.SendPhoto %v", err)
			}

		}

		var tgaudio tg.Audio
		if tgmsg, err := tg.SendAudioFile(tg.SendAudioFileRequest{
			ChatId:    channel.TgChatId,
			Performer: channel.TgPerformer,
			Title:     vtitle,
			Duration:  vinfo.Duration,
			Audio:     bytes.NewReader(audioBytes),
			Thumb:     bytes.NewReader(thumbBytes),
		}); err != nil {
			return fmt.Errorf("ERROR tg.SendAudioFile %v", err)
		} else {
			tgaudio = tgmsg.Audio
			if err := tg.DeleteMessage(tg.DeleteMessageRequest{
				ChatId:    channel.TgChatId,
				MessageId: tgmsg.MessageId,
			}); err != nil {
				perr("ERROR tg.DeleteMessage %v", err)
			}
		}

		audioCaption := tg.Esc(tg.F(
			"%s"+NL+"%s %s"+NL+"youtu.be/%s %s",
			vtitle, channel.TgPerformer, vpatime.Format("2006/01/02"), v.ResourceId.VideoId, vinfo.Duration,
		))

		if _, err := tg.SendAudio(tg.SendAudioRequest{
			ChatId:  channel.TgChatId,
			Audio:   tgaudio.FileId,
			Caption: audioCaption,
		}); err != nil {
			return fmt.Errorf("tg.SendAudio %v", err)
		}

		if !channel.TgSkipDescription {

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
				_, err = tg.SendMessage(tg.SendMessageRequest{
					ChatId: channel.TgChatId,
					Text:   tg.Esc(sp),

					LinkPreviewOptions: tg.LinkPreviewOptions{IsDisabled: true},
				})
				if err != nil {
					return fmt.Errorf("tg.SendMessage %v", err)
				}
			}

		}

		channel.YtLast = vpatime.Format(time.RFC3339)
		if err := Config.Put(); err != nil {
			return fmt.Errorf("Config.Put %v", err)
		}

		if len(videos) > 3 {
			perr("DEBUG %s sleeping <%v>", channel.YtUsername, Config.TgPlaylistVideosInterval)
			time.Sleep(Config.TgPlaylistVideosInterval)
		}
	}

	return nil
}

type ThrottledReader struct {
	Reader io.Reader
	Bps    int64 // bits per second
}

func (sr *ThrottledReader) Read(p []byte) (int, error) {
	n, err := sr.Reader.Read(p)
	if n > 0 && sr.Bps > 0 {
		time.Sleep(time.Duration(float64(n<<3) / float64(sr.Bps) * float64(time.Second)))
	}
	return n, err
}

func downloadFile(url string) ([]byte, error) {
	resp, err := HttpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bb := bytes.NewBuffer(nil)

	if _, err := io.Copy(bb, resp.Body); err != nil {
		return nil, err
	}

	return bb.Bytes(), nil
}

type UserAgentTransport struct {
	Transport http.RoundTripper
	UserAgent string
}

func (uat *UserAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", uat.UserAgent)
	return uat.Transport.RoundTrip(req)
}

func beats(td time.Duration) int {
	return int(td / BEAT)
}

func ts() string {
	tnow := time.Now().In(TZIST)
	return fmt.Sprintf(
		"%d%02d%02d:%02d%02dॐ",
		tnow.Year()%1000, tnow.Month(), tnow.Day(),
		tnow.Hour(), tnow.Minute(),
	)
}

func perr(msg string, args ...interface{}) {
	if strings.HasPrefix(msg, "DEBUG ") && !Config.DEBUG {
		return
	}
	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, ts()+" "+msg+NL, args...)
	} else {
		fmt.Fprint(os.Stderr, ts()+" "+msg+NL)
	}
}

func tglog(msg string, args ...interface{}) (err error) {
	perr(msg, args...)
	if _, err = tg.SendMessage(tg.SendMessageRequest{
		ChatId: Config.TgChatId,
		Text:   tg.Esc(tg.F(msg, args...)),

		DisableNotification: true,
		LinkPreviewOptions:  tg.LinkPreviewOptions{IsDisabled: true},
	}); err != nil {
		perr("ERROR tglog %v", err)
	}
	return err
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

	//perr("DEBUG Config.Get %+v", config)

	return nil
}

func (config *TgTubeChanConfig) Put() error {
	//perr("DEBUG Config.Put %s %+v", config.YssUrl, config)

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
