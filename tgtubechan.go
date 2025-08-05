/*

https://github.com/kkdai/youtube/v2/

https://developers.google.com/youtube/v3/docs/playlistItems/list
https://console.cloud.google.com/apis/api/youtube.googleapis.com/quotas

https://core.telegram.org/bots/api


go get github.com/kkdai/youtube/v2@master

GoGet GoFmt GoBuildNull

*/

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
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
	_ "image/png"

	ytdl "github.com/kkdai/youtube/v2"
	youtubeoption "google.golang.org/api/option"
	youtube "google.golang.org/api/youtube/v3"
	yaml "gopkg.in/yaml.v3"

	"github.com/shoce/tg"
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
		log("DEBUG <true>")
	}

	log("Interval <%v>", Config.Interval)
	if Config.Interval == 0 {
		log("ERROR Interval empty")
		os.Exit(1)
	}

	log("TgApiUrlBase [%s]", Config.TgApiUrlBase)
	if Config.TgApiUrlBase == "" {
		log("ERROR TgApiUrlBase empty")
		os.Exit(1)
	}

	tg.ApiUrl = Config.TgApiUrlBase

	if Config.TgToken == "" {
		log("ERROR TgToken empty")
		os.Exit(1)
	}

	tg.ApiToken = Config.TgToken

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
			log("ERROR TgTitleCleanRe [%s]; %v", Config.TgTitleCleanRe, err)
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

	log("FfmpegPath [%s]", Config.FfmpegPath)
	log("FfmpegGlobalOptions (%+v)", Config.FfmpegGlobalOptions)
}

func main() {
	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM)
	go func(sigterm chan os.Signal) {
		<-sigterm
		tglog("%s: sigterm", os.Args[0])
		os.Exit(1)
	}(sigterm)

	ytdl.VisitorIdMaxAge = 1 * time.Hour
	YtdlCl = ytdl.Client{
		HTTPClient: &http.Client{
			Transport: &UserAgentTransport{
				http.DefaultTransport,
				Config.YtHttpClientUserAgent,
			},
		},
	}

	for {
		t0 := time.Now()

		processYtChannel()

		if dur := time.Now().Sub(t0); dur < Config.Interval {
			time.Sleep(Config.Interval - dur)
		}
	}

	return
}

func processYtChannel() {
	var err error

	YtSvc, err = youtube.NewService(Ctx, youtubeoption.WithAPIKey(Config.YtKey))
	if err != nil {
		tglog("ERROR youtube NewService: %v", err)
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
				"channel id [%s]"+NL+"title [%s]"+NL+"uploads playlist id <%v>"+NL,
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

	sort.Slice(videos, func(i, j int) bool { return videos[i].PublishedAt < videos[j].PublishedAt })

	if Config.DEBUG {
		for j, v := range videos {
			tglog(
				"DEBUG "+NL+"<%d>/<%d> title [%s] "+NL+"url [youtu.be/%s] "+NL+"published <%s> ",
				j+1,
				len(videos),
				v.Title,
				v.ResourceId.VideoId,
				v.PublishedAt,
			)
		}
	}

	for _, v := range videos {
		var vpatime time.Time
		var vtitle string
		var audioName string

		vpatime, err = time.Parse(time.RFC3339, v.PublishedAt)
		if err != nil {
			tglog("ERROR time.Parse PublishedAt: %v", err)
			// TODO exit => return
			os.Exit(1)
		}

		vinfo, err := YtdlCl.GetVideoContext(Ctx, v.ResourceId.VideoId)
		if err != nil {
			tglog("ERROR GetVideoContext: %#v"+NL+"%#v", err, v)
			// TODO 23/5@415 New: #216 20221215.091855.8Q8QCOlhn5U: Прямая трансляция пользователя Сергей Бугаев
			// TODO 23/5@415 GetVideoContext: cannot playback and download, status: LIVE_STREAM_OFFLINE, reason: This live event will begin in a few moments.
			if _, ok := err.(*ytdl.ErrPlayabiltyStatus); ok {
				if err.(*ytdl.ErrPlayabiltyStatus).Status == "LIVE_STREAM_OFFLINE" && time.Now().Sub(vpatime) > 24*time.Hour {
					tglog("DEBUG GetVideoContext skipping LIVE_STREAM_OFFLINE youtu.be/%s", v.ResourceId.VideoId)
					continue
				}
			}
			break
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
			tglog("ERROR no thumb url")
			break
		}

		var thumbBytes []byte
		thumbBytes, err = downloadFile(thumbUrl)
		if err != nil {
			tglog("ERROR download thumb url [%s]: %v", thumbUrl, err)
			break
		}
		log("DEBUG thumb url [%s] size <%dkb>", thumbUrl, len(thumbBytes)>>10)

		if thumbImg, thumbImgFmt, err := image.Decode(bytes.NewReader(thumbBytes)); err != nil {
			tglog("ERROR thumb url [%s] decode: %v", thumbUrl, err)
		} else {
			dx, dy := thumbImg.Bounds().Dx(), thumbImg.Bounds().Dy()
			tglog("DEBUG thumb url [%s] fmt [%s] res [%dx%d]", thumbUrl, thumbImgFmt, dx, dy)
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

		var audioBuf *bytes.Buffer
		audioBuf = bytes.NewBuffer(nil)
		if _, err = io.Copy(audioBuf, ytstream); err != nil {
			tglog("ERROR copy stream: %v", err)
			break
		}

		log(
			"downloaded audio size <%dmb> bitrate <%dkbps> duration <%ds>",
			audioBuf.Len()>>20,
			audioFormat.Bitrate>>10,
			uint64(vinfo.Duration.Seconds()),
		)
		if audioBuf.Len()>>20 < 1 {
			log("WARNING downloaded audio is less than one megabyte, aborting")
			break
		}

		audioSrcFile := fmt.Sprintf("%s..m4a", audioName)
		err = ioutil.WriteFile(audioSrcFile, audioBuf.Bytes(), 0400)
		if err != nil {
			tglog("ERROR WriteFile [%s]: %v", audioSrcFile, err)
			break
		}

		audioFile := audioSrcFile

		if Config.FfmpegPath != "" && Config.TgAudioBitrateKbps > 0 {
			log("DEBUG target audio bitrate <%dkbps>", Config.TgAudioBitrateKbps)
			audioFile = fmt.Sprintf("%s..%dk..m4a", audioName, Config.TgAudioBitrateKbps)
			ffmpegArgs := append(Config.FfmpegGlobalOptions,
				"-i", audioSrcFile,
				"-b:a", fmt.Sprintf("%dk", Config.TgAudioBitrateKbps),
				audioFile,
			)
			err = exec.Command(Config.FfmpegPath, ffmpegArgs...).Run()
			if err != nil {
				tglog("ERROR ffmpeg (%s %v): %v", Config.FfmpegPath, ffmpegArgs, err)
				break
			}

			err = os.Remove(audioSrcFile)
			if err != nil {
				tglog("ERROR Remove [%s]: %v", audioSrcFile, err)
			}
		}

		audioBytes, err := ioutil.ReadFile(audioFile)
		if err != nil {
			tglog("ERROR ReadFile [%s]: %v", audioFile, err)
			break
		} else if err := os.Remove(audioFile); err != nil {
			tglog("ERROR os.Remove [%s]: %v", audioFile, err)
		}

		log("DEBUG audio size <%dmb>", len(audioBytes)>>20)

		var tgcover tg.PhotoSize
		if tgmsg, err := tg.SendPhotoFile(tg.SendPhotoFileRequest{
			ChatId:   Config.TgChatId,
			FileName: audioName,
			Photo:    bytes.NewReader(thumbBytes),
		}); err != nil {
			tglog("ERROR tg.SendPhotoFile: %v", err)
			break
		} else {
			for _, p := range tgmsg.Photo {
				if p.Width > tgcover.Width {
					tgcover = p
				}
			}
			if tgcover.FileId == "" {
				tglog("ERROR tg.SendPhotoFile: file_id empty")
				break
			}
			if err := tg.DeleteMessage(tg.DeleteMessageRequest{
				ChatId:    Config.TgChatId,
				MessageId: tgmsg.MessageId,
			}); err != nil {
				log("ERROR tg.DeleteMessage: %v", err)
			}
		}

		var tgaudio tg.Audio
		if tgmsg, err := tg.SendAudioFile(tg.SendAudioFileRequest{
			ChatId:    Config.TgChatId,
			Performer: Config.TgPerformer,
			Title:     vtitle,
			Duration:  vinfo.Duration,
			Audio:     bytes.NewReader(audioBytes),
			Thumb:     bytes.NewReader(thumbBytes),
		}); err != nil {
			tglog("ERROR tg.SendAudioFile: %v", err)
			break
		} else {
			tgaudio = tgmsg.Audio
			if err := tg.DeleteMessage(tg.DeleteMessageRequest{
				ChatId:    Config.TgChatId,
				MessageId: tgmsg.MessageId,
			}); err != nil {
				log("ERROR tg.DeleteMessage: %v", err)
			}
		}

		photoCaption := tg.BoldUnderline(vtitle)

		if _, err := tg.SendPhoto(tg.SendPhotoRequest{
			ChatId:  Config.TgChatId,
			Photo:   tgcover.FileId,
			Caption: photoCaption,
		}); err != nil {
			tglog("ERROR tg.SendPhoto: %v", err)
			break
		}

		audioCaption := tg.Esc(
			"%s"+NL+"%s"+NL+"youtu.be/%s %s",
			vtitle, Config.TgPerformer, v.ResourceId.VideoId, vinfo.Duration,
		)

		if _, err := tg.SendAudio(tg.SendAudioRequest{
			ChatId:  Config.TgChatId,
			Audio:   tgaudio.FileId,
			Caption: audioCaption,
		}); err != nil {
			tglog("ERROR tg.SendAudio: %v", err)
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
			_, err = tg.SendMessage(tg.SendMessageRequest{
				ChatId: Config.TgChatId,
				Text:   tg.Esc(sp),

				LinkPreviewOptions: tg.LinkPreviewOptions{IsDisabled: true},
			})
			if err != nil {
				tglog("ERROR tg.SendMessage: %v", err)
				break
			}
		}

		Config.YtLastPublishedAt = vpatime.Format(time.RFC3339)
		if err := Config.Put(); err != nil {
			log("ERROR Config.Put: %v", err)
			break
		}

		if len(videos) > 6 {
			log("sleeping <%v>", Config.TgVideosInterval)
			time.Sleep(Config.TgVideosInterval)
		}
	}

	return
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
	tnow := time.Now().In(time.FixedZone("IST", 330*60))
	return fmt.Sprintf(
		"%d%02d%02d:%02d%02d+",
		tnow.Year()%1000, tnow.Month(), tnow.Day(),
		tnow.Hour(), tnow.Minute(),
	)
}

func log(msg string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, ts()+" "+msg+NL, args...)
}

func tglog(msg string, args ...interface{}) (err error) {
	log(msg, args...)
	if _, err = tg.SendMessage(tg.SendMessageRequest{
		ChatId: Config.TgBossChatId,
		Text:   tg.Esc(msg, args...),

		DisableNotification: true,
		LinkPreviewOptions:  tg.LinkPreviewOptions{IsDisabled: true},
	}); err != nil {
		log("ERROR tglog: %v", err)
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

	if config.DEBUG {
		//log("DEBUG Config.Get: %+v", config)
	}

	return nil
}

func (config *TgTubeChanConfig) Put() error {
	if config.DEBUG {
		//log("DEBUG Config.Put %s %+v", config.YssUrl, config)
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
