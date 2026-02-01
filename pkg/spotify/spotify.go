package spotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	mathRand "math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/spotify"
)

var ErrNoMorePlaylist = errors.New("No more playlist")

const (
	SpotifyMaxTracksPerRequest = 100
	ServerPort                 = ":8080"
	RateLimitWaitTime          = 5
)

type AuthSpotify struct {
	Config   *oauth2.Config
	Verifier string
	Token    *oauth2.Token
	Done     chan struct{}
}

type SpotifyClient struct {
	Auth      *AuthSpotify
	ClientID  string
	WebConfig WebClient
}

type RateLimitedHTTPClient struct {
	Client      *http.Client
	RateLimiter *time.Ticker
}

type SpotifyHTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type WebClient struct {
	Client                SpotifyHTTPClient
	SpotifySearchURL      string
	SpotifyUserURL        string
	CreatePlaylistURL     string
	PlaylistURL           string
	ChangePlaylistDetails string
	GetPlaylist           string
}

type SpotifySearchResponse struct {
	Tracks TracksWrapper `json:"tracks"`
}

type TracksWrapper struct {
	Items []SpotifyItem `json:"items"`
}

type SpotifyItem struct {
	URI string `json:"uri"`
}

type PlaylistInfo struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Public      *bool  `json:"public,omitempty"`
}

type PlaylistItems struct {
	Next  string `json:"next"`
	Items []struct {
		Track struct {
			Uri string `json:"uri"`
		} `json:"track"`
	} `json:"items"`
}

type ErrorDetail struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
}

type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

type SpotifyPlaylistID struct {
	ID string `json:"id"`
}

type SpotifyUserID struct {
	ID string `json:"id"`
}

type SpotifyAddPlaylist struct {
	MusicURIS []string `json:"uris"`
}

func (c *RateLimitedHTTPClient) Do(req *http.Request) (*http.Response, error) {
	<-c.RateLimiter.C
	return c.Client.Do(req)
}

func NewAuthHandler() (*AuthSpotify, error) {
	clientID, exist := os.LookupEnv("SPOTIFY_CLIENTID")
	if !exist {
		return nil, errors.New("SPOTIFY_CLIENTID system env var not found")
	}

	clientSecret, exist := os.LookupEnv("SPOTIFY_CLIENTSECRET")
	if !exist {
		return nil, errors.New("SPOTIFY_CLIENTSECRET system env var not found")
	}

	conf := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       []string{"playlist-read-private", "playlist-read-collaborative", "playlist-modify-private", "playlist-modify-public"},
		Endpoint:     spotify.Endpoint,
		RedirectURL:  fmt.Sprintf("http://127.0.0.1%s/callback/spotify", ServerPort),
	}

	authStruct := &AuthSpotify{
		Config: conf,
		Done:   make(chan struct{}),
	}

	return authStruct, nil

}

func (a *AuthSpotify) exchangeToToken(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		fmt.Println("Code not found")
		return
	}
	token, err := a.Config.Exchange(context.Background(), code, oauth2.VerifierOption(a.Verifier))
	if err != nil {
		log.Printf("Failed to exchange token.: %s\n", err.Error())
		return
	}
	a.Token = token
	fmt.Fprintf(w, "Authenticated successfully! Access Token: %s", token.AccessToken)
	fmt.Println("Access Token:", token.AccessToken)
	close(a.Done)
}

func NewHttpServer(authStruct *AuthSpotify) (srv *http.Server) {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback/spotify", authStruct.exchangeToToken)

	srv = &http.Server{
		Addr:    ServerPort,
		Handler: mux,
	}

	return srv

}

func (a *AuthSpotify) GenerateSpotifyAuthLink() {

	a.Verifier = oauth2.GenerateVerifier()
	url := a.Config.AuthCodeURL("state",
		oauth2.AccessTypeOffline,
		oauth2.S256ChallengeOption(a.Verifier),
		oauth2.SetAuthURLParam("show_dialog", "true"),
	)
	fmt.Printf("Visit the URL for the auth dialog: %v\n", url)

}

func (s *SpotifyClient) GetUserID(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.WebConfig.SpotifyUserURL, nil)
	if err != nil {
		return fmt.Errorf("Error creating the request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.Auth.Token.AccessToken)

	resp, err := s.WebConfig.Client.Do(req)

	if err != nil {
		return fmt.Errorf("Error executing the request: %w", err)
	}
	defer resp.Body.Close()

	var response SpotifyUserID
	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		return fmt.Errorf("Error reading JSON: %w", err)
	}

	s.ClientID = response.ID
	return nil
}

func (s *SpotifyClient) EditPlaylistDescription(ctx context.Context, newPlaylistID, oldPlaylistID string) error {
	playlistInfo := PlaylistInfo{
		Description: newPlaylistID,
	}
	jsonData, err := json.Marshal(playlistInfo)
	if err != nil {
		return fmt.Errorf("Error marshaling struct: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	for {
		requestBody := bytes.NewBuffer(jsonData)

		req, err := http.NewRequestWithContext(ctx, http.MethodPut, fmt.Sprintf(s.WebConfig.ChangePlaylistDetails, oldPlaylistID), requestBody)
		if err != nil {
			return fmt.Errorf("Error creating the request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+s.Auth.Token.AccessToken)

		req.Header.Set("Content-Type", "application/json")

		resp, err := s.WebConfig.Client.Do(req)
		if err != nil {
			return fmt.Errorf("Error while doing request: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode > 299 || resp.StatusCode < 200 {
			if resp.StatusCode == 429 {
				retryAfterStr := resp.Header.Get("Retry-After")
				waitTime := RateLimitWaitTime

				if retryAfterStr != "" {
					if seconds, err := strconv.Atoi(retryAfterStr); err == nil {
						waitTime = seconds + 1
					}
				}

				jitter := mathRand.IntN(1000)
				log.Printf("Rate limit (429). Waiting %d seconds + %d ms of jitter...", waitTime, jitter)

				time.Sleep(time.Duration(waitTime)*time.Second + time.Duration(jitter)*time.Millisecond)

				continue

			}

			if resp.StatusCode == 502 {
				log.Printf("Error while adding to playlist. Trying again in %d seconds...", 1)
				time.Sleep(1 * time.Second)
				continue
			}

			var errResp ErrorResponse

			err := json.NewDecoder(resp.Body).Decode(&errResp)
			if err != nil {
				return fmt.Errorf("Error decoding JSON error: %w", err)
			}

			return fmt.Errorf("Error editing playlist (%d): %s", errResp.Error.Status, errResp.Error.Message)
		}
		break
	}
	return nil

}

func (s *SpotifyClient) CreatePlaylist(ctx context.Context, playlistInfo PlaylistInfo, oldPlaylistID string, playListCount int) (string, error) {
	if playListCount > 0 {
		playlistInfo.Name = fmt.Sprintf("%s%d", playlistInfo.Name, playListCount)
	}
	jsonData, err := json.Marshal(playlistInfo)
	if err != nil {
		return "", fmt.Errorf("Error marshaling struct: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}

	for {
		requestBody := bytes.NewBuffer(jsonData)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf(s.WebConfig.CreatePlaylistURL, s.ClientID), requestBody)
		if err != nil {

			return "", fmt.Errorf("Error creating the request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+s.Auth.Token.AccessToken)

		req.Header.Set("Content-Type", "application/json")

		resp, err := s.WebConfig.Client.Do(req)
		if err != nil {
			return "", fmt.Errorf("Error while doing request: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			if resp.StatusCode == 429 {
				retryAfterStr := resp.Header.Get("Retry-After")
				waitTime := RateLimitWaitTime

				if retryAfterStr != "" {
					if seconds, err := strconv.Atoi(retryAfterStr); err == nil {
						waitTime = seconds + 1
					}
				}

				jitter := mathRand.IntN(1000)
				log.Printf("Rate limit (429). Waiting %d seconds + %d ms of jitter...", waitTime, jitter)

				time.Sleep(time.Duration(waitTime)*time.Second + time.Duration(jitter)*time.Millisecond)

				continue
			}

			if resp.StatusCode == 502 {
				log.Printf("Error while adding to playlist. Trying again in %d seconds...", 1)
				time.Sleep(1 * time.Second)
				continue
			}

			var errResp ErrorResponse
			err := json.NewDecoder(resp.Body).Decode(&errResp)
			if err != nil {
				return "", fmt.Errorf("Error decoding JSON error: %w", err)
			}

			return "", fmt.Errorf("Error creating playlist (%d): %s", errResp.Error.Status, errResp.Error.Message)
		}
		var SpotifyID SpotifyPlaylistID
		err = json.NewDecoder(resp.Body).Decode(&SpotifyID)
		if err != nil {
			log.Println("Error retrieving Spotify ID")
			return "", fmt.Errorf("Error decoding JSON error: %w", err)
		}

		log.Println("Playlist Created")
		if playListCount > 0 {
			if oldPlaylistID == "" {
				return "", fmt.Errorf("Old Playlist ID is NULL")
			}
			err = s.EditPlaylistDescription(ctx, SpotifyID.ID, oldPlaylistID)
			if err != nil {
				return "", err
			}

		}
		return SpotifyID.ID, nil
	}

}

func (s *SpotifyClient) AddToPlaylist(ctx context.Context, musicURIS SpotifyAddPlaylist, playlistID string) error {
	jsonData, err := json.Marshal(musicURIS)
	if err != nil {

		return fmt.Errorf("Error While marshaling: %s", err)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	for {
		requestBody := bytes.NewBuffer(jsonData)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf(s.WebConfig.PlaylistURL, playlistID), requestBody)
		if err != nil {
			return fmt.Errorf("Error while creating request: %s", err)
		}

		req.Header.Set("Authorization", "Bearer "+s.Auth.Token.AccessToken)

		req.Header.Set("Content-Type", "application/json")

		resp, err := s.WebConfig.Client.Do(req)
		if err != nil {
			return fmt.Errorf("Error while requesting: %s", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode > 300 {
			if resp.StatusCode == 429 {
				retryAfterStr := resp.Header.Get("Retry-After")
				waitTime := RateLimitWaitTime

				if retryAfterStr != "" {
					if seconds, err := strconv.Atoi(retryAfterStr); err == nil {
						waitTime = seconds + 1
					}
				}

				jitter := mathRand.IntN(1000)
				log.Printf("Rate limit (429). Waiting %d seconds + %d ms of jitter...", waitTime, jitter)

				time.Sleep(time.Duration(waitTime)*time.Second + time.Duration(jitter)*time.Millisecond)

				continue

			}

			if resp.StatusCode == 502 {
				log.Printf("Error while adding to playlist. Trying again in %d seconds...", 1)
				time.Sleep(1 * time.Second)
				continue
			}
			var errResp ErrorResponse
			err = json.NewDecoder(resp.Body).Decode(&errResp)
			if err != nil {
				return fmt.Errorf("Error decoding JSON error: %s", err)
			}
			return fmt.Errorf("Error to add music to playlist `%s` (%d): %s", playlistID, errResp.Error.Status, errResp.Error.Message)
		}
		break
	}

	return nil
}

func (s *SpotifyClient) GetNextPlaylist(ctx context.Context, PlaylistID string) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(s.WebConfig.GetPlaylist, PlaylistID), nil)
		if err != nil {
			return "", fmt.Errorf("Error while creating request: %s", err)
		}

		req.Header.Set("Authorization", "Bearer "+s.Auth.Token.AccessToken)

		query := req.URL.Query()
		query.Add("fields", "description")

		req.URL.RawQuery = query.Encode()

		resp, err := s.WebConfig.Client.Do(req)
		if err != nil {
			return "", fmt.Errorf("Error while requesting: %s", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode > 300 {

			if resp.StatusCode == 429 {
				retryAfterStr := resp.Header.Get("Retry-After")
				waitTime := RateLimitWaitTime

				if retryAfterStr != "" {
					if seconds, err := strconv.Atoi(retryAfterStr); err == nil {
						waitTime = seconds + 1
					}
				}

				jitter := mathRand.IntN(1000)
				log.Printf("Rate limit (429). Waiting %d seconds + %d ms of jitter...", waitTime, jitter)

				time.Sleep(time.Duration(waitTime)*time.Second + time.Duration(jitter)*time.Millisecond)

				continue
			}

			if resp.StatusCode == 502 {
				log.Printf("Error while adding to playlist. Trying again in %d seconds...", 1)
				time.Sleep(1 * time.Second)
				continue
			}

			var errResp ErrorResponse
			err = json.NewDecoder(resp.Body).Decode(&errResp)
			if err != nil {
				return "", fmt.Errorf("Error decoding JSON error: %s", err)
			}
			return "", fmt.Errorf("Error to get playlist description `%s` (%d): %s", PlaylistID, errResp.Error.Status, errResp.Error.Message)
		}

		var description PlaylistInfo
		err = json.NewDecoder(resp.Body).Decode(&description)
		if err != nil {
			return "", fmt.Errorf("Error to decode response: %w", err)
		}

		if description.Description == "null" {
			return "", ErrNoMorePlaylist
		}

		return description.Description, nil
	}
}
