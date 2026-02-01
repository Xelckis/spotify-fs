package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"spotifyfs/pkg/job"
	"spotifyfs/pkg/spotify"
	"strings"
	"time"
)

const (
	Charset             = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	LengthRNGString     = 5
	numWorkers          = 3
	saltSize            = 16
	keySize             = 32
	RateLimitWaitTime   = 5
	maxBytesPerPlaylist = 10000
	tickerMs            = 200
)

func StringInput(question string, answer *string, optional bool) {
	for {
		fmt.Printf("%s", question)
		fmt.Scanln(answer)
		if strings.TrimSpace(*answer) == "" && !optional {
			fmt.Println("Empty answer... Please try again")
			continue
		}
		break
	}
}

func initSpotify() (spotify.SpotifyClient, error) {
	authStruct, err := spotify.NewAuthHandler()
	if err != nil {
		log.Fatalln(err)
	}
	go authStruct.GenerateSpotifyAuthLink()

	srv := spotify.NewHttpServer(authStruct)
	go func(srv *http.Server) {
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
		return spotify.SpotifyClient{}, fmt.Errorf("Server shut down due to inactivity (timeout).")
	}

	ctx = context.Background()

	ticker := time.NewTicker(tickerMs * time.Millisecond)
	webConfig := spotify.WebClient{
		Client: &spotify.RateLimitedHTTPClient{
			Client:      &http.Client{Timeout: 10 * time.Second},
			RateLimiter: ticker,
		},
		SpotifySearchURL: "https://api.spotify.com/v1/search",
		SpotifyUserURL:   "https://api.spotify.com/v1/me",

		// The `%s` prefix is required in both URLs because information such as the user ID is needed for the query.
		CreatePlaylistURL:     "https://api.spotify.com/v1/users/%s/playlists",
		PlaylistURL:           "https://api.spotify.com/v1/playlists/%s/tracks",
		ChangePlaylistDetails: "https://api.spotify.com/v1/playlists/%s",
		GetPlaylist:           "https://api.spotify.com/v1/playlists/%s",
	}

	client := spotify.SpotifyClient{
		Auth:      authStruct,
		WebConfig: webConfig,
	}

	err = client.GetUserID(ctx)
	if err != nil {
		return spotify.SpotifyClient{}, fmt.Errorf("Cannot get user ID: %v", err)
	}

	return client, nil
}

func main() {

	fmt.Printf(` 
                                                                                                      
 @@@@@@   @@@@@@@    @@@@@@   @@@@@@@  @@@  @@@@@@@@  @@@ @@@             @@@@@@@@   @@@@@@   
@@@@@@@   @@@@@@@@  @@@@@@@@  @@@@@@@  @@@  @@@@@@@@  @@@ @@@             @@@@@@@@  @@@@@@@   
!@@       @@!  @@@  @@!  @@@    @@!    @@!  @@!       @@! !@@             @@!       !@@       
!@!       !@!  @!@  !@!  @!@    !@!    !@!  !@!       !@! @!!             !@!       !@!       
!!@@!!    @!@@!@!   @!@  !@!    @!!    !!@  @!!!:!     !@!@!   @!@!@!@!@  @!!!:!    !!@@!!    
 !!@!!!   !!@!!!    !@!  !!!    !!!    !!!  !!!!!:      @!!!   !!!@!@!!!  !!!!!:     !!@!!!   
     !:!  !!:       !!:  !!!    !!:    !!:  !!:         !!:               !!:            !:!  
    !:!   :!:       :!:  !:!    :!:    :!:  :!:         :!:               :!:           !:!   
:::: ::    ::       ::::: ::     ::     ::   ::          ::                ::       :::: ::   
:: : :     :         : :  :      :     :     :           :                 :        :: : :


		`)

	var option int
	for {
		fmt.Printf("Would you like to:\n1) Write file to Playlist\n2) Read file from Playlist\nAnswer:")
		fmt.Scanln(&option)
		if option > 2 {
			fmt.Println("Invalid option... Try again")
			continue
		}
		break
	}

	var secretKey string
	StringInput("Enter password to use as a seed: ", &secretKey, false)

	var filepath string
	var playlistName string
	var playlistID string
	var gobFilePath string
	switch option {
	case 1:
		StringInput("Enter the filepath of the file you would like to store: ", &filepath, false)
		StringInput("Enter a name for the Playlist: ", &playlistName, false)
		client, err := initSpotify()
		if err != nil {
			fmt.Println(err)
			return
		}
		job.Writer(&client, filepath, secretKey, playlistName)

	case 2:
		StringInput("Enter playlist ID: ", &playlistID, false)
		StringInput("Enter a name for the file to be restored, including the extension: ", &filepath, false)
		StringInput("Path to the decoder file (Optional, but recommended): ", &gobFilePath, true)
		client, err := initSpotify()
		if err != nil {
			fmt.Println(err)
			return
		}
		job.Reader(playlistID, filepath, secretKey, gobFilePath, &client)

	default:
	}

}
