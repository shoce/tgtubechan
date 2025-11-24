/*

https://github.com/kkdai/youtube/v2/

https://developers.google.com/youtube/v3/docs/playlistItems/list
https://console.cloud.google.com/apis/api/youtube.googleapis.com/quotas

https://core.telegram.org/bots/api


go get github.com/kkdai/youtube/v2@master

GoGet GoFmt GoBuildNull

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

	TgApiUrlBase string `yaml:"TgApiUrlBase"` // = "https://api.telegram.org"

	TgToken  string `yaml:"TgToken"`
	TgChatId string `yaml:"TgChatId"`

	TgPlaylistVideosInterval time.Duration `yaml:"TgPlaylistVideosInterval"`

	TgAudioBitrateKbps int64 `yaml:"TgAudioBitrateKbps"` // = 60

	YtKey        string `yaml:"YtKey"`
	YtMaxResults int64  `yaml:"YtMaxResults"` // = 50
	YtThrottle   int64  `yaml:"YtThrottle"`   // = 12

	YtUserAgent string `yaml:"YtUserAgent"` // = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.6 Safari/605.1.15"

	FfmpegPath          string   `yaml:"FfmpegPath"`          // = "/bin/ffmpeg"
	FfmpegGlobalOptions []string `yaml:"FfmpegGlobalOptions"` // = []string{"-v", "panic"}

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

	BTOI = map[bool]int{false: 0, true: 1}
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
		log("ERROR Config.Get %v", err)
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

	if Config.TgChatId == "" {
		log("ERROR TgChatId empty")
		os.Exit(1)
	}

	if Config.TgChatId == "" {
		tglog("ERROR TgChatId empty")
		os.Exit(1)
	}

	if Config.TgPlaylistVideosInterval == 0 {
		log("ERROR TgPlaylistVideosInterval empty")
		os.Exit(1)
	}

	if Config.TgAudioBitrateKbps == 0 {
		log("ERROR TgAudioBitrateKbps empty")
		os.Exit(1)
	}

	for i, channel := range Config.Channels {
		if channel.YtUsername == "" {
			log("ERROR Channel <%d> YtUsername empty", i)
			os.Exit(1)
		}
		if channel.TgTitleCleanRe != "" {
			if _, err := regexp.Compile(channel.TgTitleCleanRe); err != nil {
				log("ERROR Channel %s TgTitleCleanRe [%s] %v", channel.YtUsername, channel.TgTitleCleanRe, err)
				os.Exit(1)
			}
		}
	}

	if Config.YtKey == "" {
		tglog("ERROR YtKey empty")
		os.Exit(1)
	}

	if YtSvc, err = youtube.NewService(Ctx, youtubeoption.WithAPIKey(Config.YtKey)); err != nil {
		tglog("ERROR youtube NewService %v", err)
		os.Exit(1)
	}

	if Config.YtMaxResults == 0 {
		Config.YtMaxResults = 50
	}

	if Config.YtThrottle == 0 {
		Config.YtThrottle = 12
	}
	log("YtThrottle <%d>", Config.YtThrottle)

	//log("FfmpegPath [%s]", Config.FfmpegPath)
	//log("FfmpegGlobalOptions (%+v)", Config.FfmpegGlobalOptions)

	log("Channels (")
	for _, channel := range Config.Channels {
		log("{ @Suspend <%v> @YtUsername %s @YtLast <%s> }", BTOI[channel.Suspend], channel.YtUsername, channel.YtLast)
	}
	log(")")

	ytdl.VisitorIdMaxAge = 33 * time.Minute

	YtdlCl = ytdl.Client{
		HTTPClient: &http.Client{
			Transport: &UserAgentTransport{
				http.DefaultTransport,
				Config.YtUserAgent,
			},
		},
	}

	rand.Seed(time.Now().UnixNano())
}

func main() {
	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM)
	go func(sigterm chan os.Signal) {
		<-sigterm
		tglog("%s sigterm", os.Args[0])
		os.Exit(1)
	}(sigterm)

	channels := Config.Channels

	ticker := time.NewTicker(Config.Interval)

	for {
		rand.Shuffle(len(channels), func(i, j int) {
			channels[i], channels[j] = channels[j], channels[i]
		})

		for jchannel, _ := range channels {
			channel := &channels[jchannel]
			if channel.Suspend {
				log("DEBUG %s Suspend <true>", channel.YtUsername)
				continue
			} else {
				log("DEBUG %s", channel.YtUsername)
			}
			if err := processYtChannel(channel); err != nil {
				tglog("ERROR %s %v", channel.YtUsername, err)
			}
		}

		log("DEBUG sleeping")
		<-ticker.C
	}

	return
}

func processYtChannel(channel *TgTubeChanChannel) (err error) {
	if channel.YtPlaylistId == "" {
		if channel.YtUsername == "" && channel.YtChannelId == "" {
			return fmt.Errorf("Empty YtUsername and YtChannelId, nothing to do")
		}

		// https://developers.google.com/youtube/v3/docs/channels/list

		channelslistcall := YtSvc.Channels.List([]string{"id", "snippet", "contentDetails"}).MaxResults(11)
		if channel.YtChannelId != "" {
			channelslistcall = channelslistcall.Id(channel.YtChannelId)
		} else if channel.YtUsername != "" {
			channelslistcall = channelslistcall.ForUsername(channel.YtUsername)
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

	for j, v := range videos {
		log(
			"DEBUG %s <%d>/<%d> title [%s] url [youtu.be/%s] published <%s>",
			channel.YtUsername, j+1, len(videos), v.Title, v.ResourceId.VideoId, v.PublishedAt,
		)

		var vpatime time.Time
		var vtitle string
		var audioName string

		if vpatime, err = time.Parse(time.RFC3339, v.PublishedAt); err != nil {
			return fmt.Errorf("time.Parse PublishedAt [%s] %v", v.PublishedAt, err)
		}

		vinfo, err := YtdlCl.GetVideoContext(Ctx, v.ResourceId.VideoId)
		if err != nil {

			// cannot playback and download, status: LIVE_STREAM_OFFLINE, reason: This live event will begin in a few moments.
			if _, ok := err.(*ytdl.ErrPlayabiltyStatus); ok {
				if err.(*ytdl.ErrPlayabiltyStatus).Status == "LIVE_STREAM_OFFLINE" && time.Now().Sub(vpatime) > 24*time.Hour {
					log("DEBUG GetVideoContext skipping LIVE_STREAM_OFFLINE youtu.be/%s", v.ResourceId.VideoId)
					continue
				}
			}

			// can't bypass age restriction: embedding of this video has been disabled
			if err2 := errors.Unwrap(err); err2 != nil && err2.Error() == "embedding of this video has been disabled" {
				tgmsg := tg.Italic("embedding of this video has been disabled") + NL +
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
					log("Config.Put %v", err)
				}

				continue
			}

			// login required to confirm your age
			if err.Error() == "login required to confirm your age" {
				tgmsg := tg.Italic("login required to confirm your age") + NL +
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
					log("Config.Put %v", err)
				}

				continue
			}

			return fmt.Errorf("GetVideoContext %#v"+NL+"%#v", err, v)

		}

		vtitle = v.Title

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

		audioName = fmt.Sprintf(
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
			if Config.DEBUG {
				log("DEBUG format size <%dmb> AudioTrack %+v", f.ContentLength>>20, f.AudioTrack)
			}
			if f.AudioTrack != nil && !strings.HasSuffix(f.AudioTrack.DisplayName, " original") {
				continue
			}
			if audioFormat.Bitrate == 0 || f.Bitrate > audioFormat.Bitrate {
				if Config.DEBUG {
					log("DEBUG pick")
				}
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

		if Config.DEBUG {
			log("DEBUG downloaded audio size <%dmb> bitrate <%dkbps> duration <%v> in <%v>",
				audioBuf.Len()>>20, audioFormat.Bitrate>>10, vinfo.Duration, time.Now().Sub(t0dl).Truncate(time.Second),
			)
		}

		if expectsize := int(vinfo.Duration.Seconds()) * audioFormat.Bitrate / 8; audioBuf.Len() < expectsize/2 {
			return fmt.Errorf("downloaded audio size is less than half of expected")
		}

		audioSrcFile := fmt.Sprintf("%s..m4a", audioName)
		if err = ioutil.WriteFile(audioSrcFile, audioBuf.Bytes(), 0400); err != nil {
			return fmt.Errorf("WriteFile %s %v", audioSrcFile, err)
		}

		audioFile := audioSrcFile

		if Config.FfmpegPath != "" && Config.TgAudioBitrateKbps > 0 {
			if Config.DEBUG {
				log("DEBUG target audio bitrate <%dkbps>", Config.TgAudioBitrateKbps)
			}
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
			log("ERROR thumb url [%s] decode %v", thumbUrl, err)
		} else {
			dx, dy := thumbImg.Bounds().Dx(), thumbImg.Bounds().Dy()
			if Config.DEBUG {
				log("DEBUG thumb url [%s] fmt [%s] size <%dkb> res <%dx%d>", thumbUrl, thumbImgFmt, len(thumbBytes)>>10, dx, dy)
			}
		}

		if !channel.TgSkipPhoto {

			var tgcover tg.PhotoSize
			if tgmsg, err := tg.SendPhotoFile(tg.SendPhotoFileRequest{
				ChatId:   channel.TgChatId,
				FileName: audioName,
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
					log("ERROR tg.DeleteMessage: %v", err)
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
				log("ERROR tg.DeleteMessage %v", err)
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
			if Config.DEBUG {
				log("DEBUG %s sleeping <%v>", channel.YtUsername, Config.TgPlaylistVideosInterval)
			}
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

func log(msg string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, ts()+" "+msg+NL, args...)
}

func tglog(msg string, args ...interface{}) (err error) {
	log(msg, args...)
	if _, err = tg.SendMessage(tg.SendMessageRequest{
		ChatId: Config.TgChatId,
		Text:   tg.Esc(tg.F(msg, args...)),

		DisableNotification: true,
		LinkPreviewOptions:  tg.LinkPreviewOptions{IsDisabled: true},
	}); err != nil {
		log("ERROR tglog %v", err)
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
		//log("DEBUG Config.Get %+v", config)
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
