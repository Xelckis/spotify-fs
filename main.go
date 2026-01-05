package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"

	"log"
	"net/http"
	"os"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/spotify"
)

const ServerPort = ":8080"
const Charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
const LengthRNGString = 3
const SpotifyMaxTracksPerRequest = 100

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

type SpotifyHTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type WebClient struct {
	client                SpotifyHTTPClient
	SpotifySearchURL      string
	SpotifyUserURL        string
	CreatePlaylistURL     string
	AddPlaylistURL        string
	ChangePlaylistDetails string
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
	Name        string `json:"name"`
	Description string `json:"description"`
	Public      bool   `json:"public"`
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

func (a *AuthSpotify) generateSpotifyAuthLink() {

	a.Verifier = oauth2.GenerateVerifier()
	url := a.Config.AuthCodeURL("state", oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(a.Verifier))
	fmt.Printf("Visit the URL for the auth dialog: %v", url)

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

func (s *SpotifyClient) GetUserID(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.WebConfig.SpotifyUserURL, nil)
	if err != nil {
		return fmt.Errorf("Error creating the request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.Auth.Token.AccessToken)

	resp, err := s.WebConfig.client.Do(req)

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

func NewHttpServer(authStruct *AuthSpotify) (srv *http.Server) {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback/spotify", authStruct.exchangeToToken)

	srv = &http.Server{
		Addr:    ServerPort,
		Handler: mux,
	}

	return srv

}

func NewRNGStringWithSeed(length int, hash []byte, modifier uint64) string {
	baseSeed := binary.BigEndian.Uint64(hash)

	seed := baseSeed + modifier

	source := rand.NewPCG(seed, 0)
	r := rand.New(source)

	charset := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var sb strings.Builder
	sb.Grow(length)

	for i := 0; i < length; i++ {
		randomIndex := r.IntN(len(charset))
		sb.WriteByte(charset[randomIndex])
	}
	return sb.String()
}

func (s *SpotifyClient) NewDictionary(ctx context.Context, hash []byte) (map[byte]string, map[string]byte, error) {
	foundCount := 0
	byteCount := byte(0)
	seedDiff := uint64(0)
	writerDictionary := make(map[byte]string, 256)
	readerDictionary := make(map[string]byte, 256)

	if len(hash) < 8 {
		return nil, nil, fmt.Errorf("Hash has less than 8 bytes")
	}

	for foundCount <= 255 {
		err := func() error {
			searchString := NewRNGStringWithSeed(LengthRNGString, hash[:8], seedDiff)

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.WebConfig.SpotifySearchURL, nil)
			if err != nil {
				log.Printf("Error creating the request: %s\n Trying Again...", err)
				return nil
			}

			req.Header.Set("Authorization", "Bearer "+s.Auth.Token.AccessToken)

			query := req.URL.Query()
			query.Add("q", searchString)
			query.Add("type", "track")
			query.Add("limit", "1")
			query.Add("market", "US")

			req.URL.RawQuery = query.Encode()

			resp, err := s.WebConfig.client.Do(req)
			if err != nil {
				if ctx.Err() != nil {
					return fmt.Errorf("Error with context: %s", ctx.Err().Error())
				}
				log.Printf("Error executing the request: %s\n Trying Again...", err)
				return nil
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				log.Printf("Unexpected status: %d\n Trying Again...", resp.StatusCode)
				return nil
			}

			var response SpotifySearchResponse
			err = json.NewDecoder(resp.Body).Decode(&response)
			if err != nil {
				log.Printf("Error reading JSON: %v\n Trying Again...", err)
				return nil
			}

			if len(response.Tracks.Items) > 0 {
				writerDictionary[byteCount] = response.Tracks.Items[0].URI
				readerDictionary[response.Tracks.Items[0].URI] = byteCount
				byteCount++
				foundCount++
			}

			return nil
		}()
		log.Printf("Music %d/255\n", foundCount)

		seedDiff++

		if err != nil {
			return nil, nil, err
		}
	}

	return writerDictionary, readerDictionary, nil
}

func (s *SpotifyClient) EditPlaylistDescription(ctx context.Context, newPlaylistID, oldPlaylistID string) error {
	playlistInfo := PlaylistInfo{
		Description: newPlaylistID,
	}
	jsonData, err := json.Marshal(playlistInfo)
	if err != nil {
		return fmt.Errorf("Error marshaling struct: %w", err)
	}

	requestBody := bytes.NewBuffer(jsonData)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, fmt.Sprintf(s.WebConfig.ChangePlaylistDetails, oldPlaylistID), requestBody)
	if err != nil {
		return fmt.Errorf("Error creating the request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.Auth.Token.AccessToken)

	req.Header.Set("Content-Type", "application/json")

	resp, err := s.WebConfig.client.Do(req)
	if err != nil {
		return fmt.Errorf("Error while doing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode > 299 || resp.StatusCode < 200 {

		var errResp ErrorResponse

		err := json.NewDecoder(resp.Body).Decode(&errResp)
		if err != nil {
			return fmt.Errorf("Error decoding JSON error: %w", err)
		}

		return fmt.Errorf("Error editing playlist (%d): %s", errResp.Error.Status, errResp.Error.Message)
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

	requestBody := bytes.NewBuffer(jsonData)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf(s.WebConfig.CreatePlaylistURL, s.ClientID), requestBody)
	if err != nil {

		return "", fmt.Errorf("Error creating the request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.Auth.Token.AccessToken)

	req.Header.Set("Content-Type", "application/json")

	resp, err := s.WebConfig.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Error while doing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {

		var errResp ErrorResponse
		log.Println("Error")
		err := json.NewDecoder(resp.Body).Decode(&errResp)
		if err != nil {
			return "", fmt.Errorf("Error decoding JSON error: %w", err)
		}

		return "", fmt.Errorf("Error creating playlist (%d): %s", errResp.Error.Status, errResp.Error.Message)
	}
	var SpotifyID SpotifyPlaylistID
	err = json.NewDecoder(resp.Body).Decode(&SpotifyID)
	if err != nil {
		log.Println("Error spotify ID")
		return "", fmt.Errorf("Error decoding JSON error: %w", err)
	}

	log.Println("Playlist Criada")
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

func (s *SpotifyClient) AddToPlaylist(ctx context.Context, musicURIS SpotifyAddPlaylist, playlistID string) error {
	jsonData, err := json.Marshal(musicURIS)
	if err != nil {

		return fmt.Errorf("Error While marshaling: %s", err)
	}

	requestBody := bytes.NewBuffer(jsonData)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf(s.WebConfig.AddPlaylistURL, playlistID), requestBody)
	if err != nil {
		return fmt.Errorf("Error while creating request: %s", err)
	}

	req.Header.Set("Authorization", "Bearer "+s.Auth.Token.AccessToken)

	req.Header.Set("Content-Type", "application/json")

	resp, err := s.WebConfig.client.Do(req)
	if err != nil {
		return fmt.Errorf("Error while requesting: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 300 {
		var errResp ErrorResponse
		err = json.NewDecoder(resp.Body).Decode(&errResp)
		if err != nil {
			return fmt.Errorf("Error decoding JSON error: %s", err)
		}
		return fmt.Errorf("Error to add music to playlist `%s` (%d): %s", playlistID, errResp.Error.Status, errResp.Error.Message)
	}

	return nil
}

func (s *SpotifyClient) Writer(filepath string, hash []byte) {
	ctx := context.Background()
	writerdictionary, _, err := s.NewDictionary(ctx, hash)
	if err != nil {
		fmt.Println(err)
	}

	file, err := os.Open(filepath)
	if err != nil {
		log.Printf("Error opening file: %v\n", err)
		return
	}
	defer file.Close()

	playlistInfo := PlaylistInfo{
		Name:        "Test",
		Description: "Test",
		Public:      true,
	}

	playlistID, err := s.CreatePlaylist(ctx, playlistInfo, "", 0)
	if err != nil {
		log.Println(err)
	}

	log.Println("Playlist ID: " + playlistID)

	buf := make([]byte, SpotifyMaxTracksPerRequest)
	bytesRead := 0
	playlistCount := 0
	for {
		if bytesRead%10000 == 0 {
			oldPlaylistID := playlistID
			playlistCount++
			playlistID, err = s.CreatePlaylist(ctx, playlistInfo, oldPlaylistID, playlistCount)
			if err != nil {
				log.Printf("Error while creating the playlist %d: %s", playlistCount, err)
				return
			}
		}
		n, err := file.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("Error reading file: %v\n", err)
			break
		}
		musicsURI := make([]string, n)

		for i := 0; i < n; i++ {
			byte := buf[i]
			musicsURI[i] = writerdictionary[byte]
		}

		addPlaylistURIS := SpotifyAddPlaylist{
			MusicURIS: musicsURI,
		}

		err = s.AddToPlaylist(ctx, addPlaylistURIS, playlistID)
		if err != nil {
			log.Println(err)
			return
		}

		fmt.Printf("Read %d bytes\n", n)
		bytesRead += n
	}
}

func main() {
	authStruct, err := NewAuthHandler()
	if err != nil {
		log.Fatalln(err)
	}
	go authStruct.generateSpotifyAuthLink()

	srv := NewHttpServer(authStruct)
	go func(srv *http.Server) {
		fmt.Printf("Server running on port %s...", ServerPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Error starting server: %s", err)
		}
	}(srv)

	var timeout bool
	select {
	case <-authStruct.Done:
		fmt.Println("Token recived, shuting down server...")
	case <-time.After(1 * time.Minute):
		fmt.Println("Timeout, shuting down server...")
		timeout = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		fmt.Printf("Error to shutdown the web server: %v\n", err)
	} else {
		fmt.Println("Server is shutdown")
	}

	if timeout {
		return
	}

	ctx = context.Background()

	webConfig := WebClient{
		client:           &http.Client{Timeout: 10 * time.Second},
		SpotifySearchURL: "https://api.spotify.com/v1/search",
		SpotifyUserURL:   "https://api.spotify.com/v1/me",

		// The `%s` prefix is required in both URLs because information such as the user ID is needed for the query.
		CreatePlaylistURL:     "https://api.spotify.com/v1/users/%s/playlists",
		AddPlaylistURL:        "https://api.spotify.com/v1/playlists/%s/tracks",
		ChangePlaylistDetails: "https://api.spotify.com/v1/playlists/%s",
	}

	client := &SpotifyClient{
		Auth:      authStruct,
		WebConfig: webConfig,
	}

	err = client.GetUserID(ctx)
	if err != nil {
		log.Println("Cannot get user ID:", err)
	}

	log.Println("User ID: ", client.ClientID)

	secretKey := "Minha-Chave-Secreta"
	h := sha256.New()
	h.Write([]byte(secretKey))
	hash := h.Sum(nil)

	client.Writer("LICENSE", hash)

}
