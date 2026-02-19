package youtube

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
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

var ErrNoMorePlaylist = errors.New("No more playlist")

const (
	YouTubeChunkSize  = 50
	ServerPort        = ":8080"
	RateLimitWaitTime = 5
	YouTubeAPIBase    = "https://www.googleapis.com/youtube/v3"
)

// --- Auth ---

type AuthYouTube struct {
	Config   *oauth2.Config
	Verifier string
	Token    *oauth2.Token
	Done     chan struct{}
}

type YouTubeClient struct {
	Auth      *AuthYouTube
	ChannelID string
	WebConfig WebClient
}

type RateLimitedHTTPClient struct {
	Client      *http.Client
	RateLimiter *time.Ticker
}

type YouTubeHTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type WebClient struct {
	Client           YouTubeHTTPClient
	SearchURL        string
	PlaylistsURL     string
	PlaylistItemsURL string
	ChannelsURL      string
}

// --- API Response Types ---

type YouTubeSearchResponse struct {
	Items []YouTubeSearchItem `json:"items"`
}

type YouTubeSearchItem struct {
	ID YouTubeSearchItemID `json:"id"`
}

type YouTubeSearchItemID struct {
	VideoID string `json:"videoId"`
}

type PlaylistSnippet struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type PlaylistStatus struct {
	PrivacyStatus string `json:"privacyStatus"`
}

type PlaylistResource struct {
	ID      string          `json:"id,omitempty"`
	Snippet PlaylistSnippet `json:"snippet"`
	Status  *PlaylistStatus `json:"status,omitempty"`
}

type PlaylistListResponse struct {
	Items []PlaylistResource `json:"items"`
}

type ResourceID struct {
	Kind    string `json:"kind"`
	VideoID string `json:"videoId"`
}

type PlaylistItemSnippet struct {
	PlaylistID string     `json:"playlistId"`
	ResourceID ResourceID `json:"resourceId"`
}

type PlaylistItemInsert struct {
	Snippet PlaylistItemSnippet `json:"snippet"`
}

type PlaylistItemEntry struct {
	Snippet PlaylistItemSnippet `json:"snippet"`
}

type PlaylistItemListResponse struct {
	NextPageToken string              `json:"nextPageToken"`
	Items         []PlaylistItemEntry `json:"items"`
}

type YouTubeErrorDetail struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type YouTubeErrorResponse struct {
	Error YouTubeErrorDetail `json:"error"`
}

// --- Rate Limiting ---

func (c *RateLimitedHTTPClient) Do(req *http.Request) (*http.Response, error) {
	<-c.RateLimiter.C
	return c.Client.Do(req)
}

// --- Auth Functions ---

func NewAuthHandler() (*AuthYouTube, error) {
	clientID, exist := os.LookupEnv("YOUTUBE_CLIENTID")
	if !exist {
		return nil, errors.New("YOUTUBE_CLIENTID system env var not found")
	}

	clientSecret, exist := os.LookupEnv("YOUTUBE_CLIENTSECRET")
	if !exist {
		return nil, errors.New("YOUTUBE_CLIENTSECRET system env var not found")
	}

	conf := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		Endpoint:     google.Endpoint,
		RedirectURL:  fmt.Sprintf("http://127.0.0.1%s/callback/youtube", ServerPort),
	}

	authStruct := &AuthYouTube{
		Config: conf,
		Done:   make(chan struct{}),
	}

	return authStruct, nil
}

func (a *AuthYouTube) exchangeToToken(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		fmt.Println("Code not found")
		return
	}
	token, err := a.Config.Exchange(context.Background(), code, oauth2.VerifierOption(a.Verifier))
	if err != nil {
		log.Printf("Failed to exchange token: %s\n", err.Error())
		return
	}
	a.Token = token
	fmt.Fprintf(w, "Authenticated successfully! You can close this window.")
	fmt.Println("Access Token:", token.AccessToken)
	close(a.Done)
}

func NewHttpServer(authStruct *AuthYouTube) (srv *http.Server) {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback/youtube", authStruct.exchangeToToken)

	srv = &http.Server{
		Addr:    ServerPort,
		Handler: mux,
	}

	return srv
}

func (a *AuthYouTube) GenerateYouTubeAuthLink() {
	a.Verifier = oauth2.GenerateVerifier()
	url := a.Config.AuthCodeURL("state",
		oauth2.AccessTypeOffline,
		oauth2.S256ChallengeOption(a.Verifier),
		oauth2.SetAuthURLParam("prompt", "consent"),
	)
	fmt.Printf("Visit the URL for the auth dialog: %v\n", url)
}

// --- Helper: handle rate limit / retry ---

func handleRateLimit(resp *http.Response) {
	jitter := mathRand.IntN(1000)
	log.Printf("Rate limit (%d). Waiting %d seconds + %d ms of jitter...", resp.StatusCode, RateLimitWaitTime, jitter)
	time.Sleep(time.Duration(RateLimitWaitTime)*time.Second + time.Duration(jitter)*time.Millisecond)
}

func isRetryable(statusCode int) bool {
	return statusCode == 429 || statusCode == 403 || statusCode == 502 || statusCode == 503
}

// --- Client Methods ---

func (s *YouTubeClient) GetChannelID(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.WebConfig.ChannelsURL, nil)
	if err != nil {
		return fmt.Errorf("Error creating the request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.Auth.Token.AccessToken)

	query := req.URL.Query()
	query.Add("part", "snippet")
	query.Add("mine", "true")
	req.URL.RawQuery = query.Encode()

	resp, err := s.WebConfig.Client.Do(req)
	if err != nil {
		return fmt.Errorf("Error executing the request: %w", err)
	}
	defer resp.Body.Close()

	var response ChannelListResponse
	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		return fmt.Errorf("Error reading JSON: %w", err)
	}

	if len(response.Items) == 0 {
		return errors.New("No channel found for authenticated user")
	}

	s.ChannelID = response.Items[0].ID
	return nil
}

type ChannelListResponse struct {
	Items []struct {
		ID string `json:"id"`
	} `json:"items"`
}

func (s *YouTubeClient) CreatePlaylist(ctx context.Context, playlistName string, oldPlaylistID string, playListCount int) (string, error) {
	title := playlistName
	if playListCount > 0 {
		title = fmt.Sprintf("%s%d", playlistName, playListCount)
	}

	resource := PlaylistResource{
		Snippet: PlaylistSnippet{
			Title:       title,
			Description: "null",
		},
		Status: &PlaylistStatus{
			PrivacyStatus: "unlisted",
		},
	}

	jsonData, err := json.Marshal(resource)
	if err != nil {
		return "", fmt.Errorf("Error marshaling struct: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}

	for {
		requestBody := bytes.NewBuffer(jsonData)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.WebConfig.PlaylistsURL+"?part=snippet,status", requestBody)
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
			if isRetryable(resp.StatusCode) {
				handleRateLimit(resp)
				continue
			}

			var errResp YouTubeErrorResponse
			json.NewDecoder(resp.Body).Decode(&errResp)
			return "", fmt.Errorf("Error creating playlist (%d): %s", errResp.Error.Code, errResp.Error.Message)
		}

		var created PlaylistResource
		err = json.NewDecoder(resp.Body).Decode(&created)
		if err != nil {
			return "", fmt.Errorf("Error decoding response: %w", err)
		}

		log.Println("Playlist Created:", created.ID)

		if playListCount > 0 {
			if oldPlaylistID == "" {
				return "", fmt.Errorf("Old Playlist ID is NULL")
			}
			err = s.EditPlaylistDescription(ctx, created.ID, oldPlaylistID)
			if err != nil {
				return "", err
			}
		}
		return created.ID, nil
	}
}

func (s *YouTubeClient) AddToPlaylist(ctx context.Context, videoID string, playlistID string) error {
	item := PlaylistItemInsert{
		Snippet: PlaylistItemSnippet{
			PlaylistID: playlistID,
			ResourceID: ResourceID{
				Kind:    "youtube#video",
				VideoID: videoID,
			},
		},
	}

	jsonData, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("Error while marshaling: %s", err)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	for {
		requestBody := bytes.NewBuffer(jsonData)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.WebConfig.PlaylistItemsURL+"?part=snippet", requestBody)
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
			if isRetryable(resp.StatusCode) {
				handleRateLimit(resp)
				continue
			}
			var errResp YouTubeErrorResponse
			json.NewDecoder(resp.Body).Decode(&errResp)
			return fmt.Errorf("Error adding video to playlist `%s` (%d): %s", playlistID, errResp.Error.Code, errResp.Error.Message)
		}
		break
	}

	return nil
}

func (s *YouTubeClient) EditPlaylistDescription(ctx context.Context, newPlaylistID, oldPlaylistID string) error {
	// First, GET the current playlist to retrieve its title (required for PUT)
	currentTitle, err := s.getPlaylistTitle(ctx, oldPlaylistID)
	if err != nil {
		return fmt.Errorf("Error getting playlist title: %w", err)
	}

	resource := PlaylistResource{
		ID: oldPlaylistID,
		Snippet: PlaylistSnippet{
			Title:       currentTitle,
			Description: newPlaylistID,
		},
	}

	jsonData, err := json.Marshal(resource)
	if err != nil {
		return fmt.Errorf("Error marshaling struct: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	for {
		requestBody := bytes.NewBuffer(jsonData)

		req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.WebConfig.PlaylistsURL+"?part=snippet", requestBody)
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

		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			if isRetryable(resp.StatusCode) {
				handleRateLimit(resp)
				continue
			}

			var errResp YouTubeErrorResponse
			json.NewDecoder(resp.Body).Decode(&errResp)
			return fmt.Errorf("Error editing playlist (%d): %s", errResp.Error.Code, errResp.Error.Message)
		}
		break
	}
	return nil
}

func (s *YouTubeClient) getPlaylistTitle(ctx context.Context, playlistID string) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.WebConfig.PlaylistsURL, nil)
		if err != nil {
			return "", fmt.Errorf("Error creating request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+s.Auth.Token.AccessToken)

		query := req.URL.Query()
		query.Add("part", "snippet")
		query.Add("id", playlistID)
		req.URL.RawQuery = query.Encode()

		resp, err := s.WebConfig.Client.Do(req)
		if err != nil {
			return "", fmt.Errorf("Error executing request: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			if isRetryable(resp.StatusCode) {
				handleRateLimit(resp)
				continue
			}
			return "", fmt.Errorf("Error fetching playlist: %d", resp.StatusCode)
		}

		var response PlaylistListResponse
		json.NewDecoder(resp.Body).Decode(&response)

		if len(response.Items) == 0 {
			return "", fmt.Errorf("Playlist not found: %s", playlistID)
		}

		return response.Items[0].Snippet.Title, nil
	}
}

func (s *YouTubeClient) GetNextPlaylist(ctx context.Context, PlaylistID string) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.WebConfig.PlaylistsURL, nil)
		if err != nil {
			return "", fmt.Errorf("Error while creating request: %s", err)
		}

		req.Header.Set("Authorization", "Bearer "+s.Auth.Token.AccessToken)

		query := req.URL.Query()
		query.Add("part", "snippet")
		query.Add("id", PlaylistID)
		req.URL.RawQuery = query.Encode()

		resp, err := s.WebConfig.Client.Do(req)
		if err != nil {
			return "", fmt.Errorf("Error while requesting: %s", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			if isRetryable(resp.StatusCode) {
				handleRateLimit(resp)
				continue
			}

			var errResp YouTubeErrorResponse
			json.NewDecoder(resp.Body).Decode(&errResp)
			return "", fmt.Errorf("Error getting playlist `%s` (%d): %s", PlaylistID, errResp.Error.Code, errResp.Error.Message)
		}

		var response PlaylistListResponse
		json.NewDecoder(resp.Body).Decode(&response)

		if len(response.Items) == 0 {
			return "", ErrNoMorePlaylist
		}

		description := response.Items[0].Snippet.Description
		if description == "" || description == "null" {
			return "", ErrNoMorePlaylist
		}

		return description, nil
	}
}
