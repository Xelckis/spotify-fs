package job

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	mathRand "math/rand/v2"
	"net/http"
	"os"
	"spotifyfs/pkg/crypto"
	"spotifyfs/pkg/spotify"
	"strconv"
	"sync"
	"time"
)

const (
	numWorkers          = 3
	maxBytesPerPlaylist = 10000
)

type WriteJob struct {
	PlaylistID string
	Chunks     [][]byte
}

type ReadJob struct {
	Sequence   int
	PlaylistID string
}

type ReadResult struct {
	Sequence int
	Data     []byte
	NextID   string
}

func WriterWorker(ctx context.Context, s *spotify.SpotifyClient, job <-chan WriteJob, writerdictionary map[byte]string, wg *sync.WaitGroup) {
	defer wg.Done()
	for j := range job {
		for i, chunk := range j.Chunks {
			musicsURI := make([]string, len(chunk))
			for idx, b := range chunk {
				musicsURI[idx] = writerdictionary[b]
			}

			addPlaylistURIS := spotify.SpotifyAddPlaylist{
				MusicURIS: musicsURI,
			}

			for {
				err := s.AddToPlaylist(ctx, addPlaylistURIS, j.PlaylistID)
				if err == nil {
					break
				}

				log.Printf("[Worker] Critical error adding chunk %d to playlist %s: %v", i, j.PlaylistID, err)
				time.Sleep(1 * time.Second)
			}
		}
		fmt.Printf("Successfully finished all chunks for playlist %s\n", j.PlaylistID)
	}
}

func Writer(s *spotify.SpotifyClient, filepath string, password string, playlistName string) {
	ctx := context.Background()
	writerdictionary, readerdictionary, err := crypto.NewDictionary(ctx, password, s)
	if err != nil {
		fmt.Printf("Error initializing dictionary: %v\n", err)
		return
	}

	fmt.Println("Saving map to file...")
	decoderFile := playlistName + "_Decoder.gob"
	if err := crypto.SaveMap(decoderFile, readerdictionary, password); err != nil {
		fmt.Printf("Error saving decoder map: %v\n", err)
		return
	}

	file, err := os.Open(filepath)
	if err != nil {
		log.Printf("Error opening file: %v\n", err)
		return
	}
	defer file.Close()

	jobs := make(chan WriteJob, numWorkers)
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for w := 0; w < numWorkers; w++ {
		go WriterWorker(ctx, s, jobs, writerdictionary, &wg)
	}

	playlistCount := 0
	lastPlaylistID := ""
	var currentChunks [][]byte
	bytesInCurrentPlaylist := 0
	readBuf := make([]byte, spotify.SpotifyMaxTracksPerRequest)

	isPublic := true
	pInfo := spotify.PlaylistInfo{
		Name:   playlistName,
		Public: &isPublic,
	}

	for {
		n, err := file.Read(readBuf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, readBuf[:n])
			currentChunks = append(currentChunks, chunk)
			bytesInCurrentPlaylist += n
		}

		if (bytesInCurrentPlaylist >= maxBytesPerPlaylist || err == io.EOF) && len(currentChunks) > 0 {
			newPlaylistID, createErr := s.CreatePlaylist(ctx, pInfo, lastPlaylistID, playlistCount)
			if createErr != nil {
				log.Printf("Failed to create playlist %d: %v", playlistCount, createErr)
				break
			}

			jobs <- WriteJob{
				PlaylistID: newPlaylistID,
				Chunks:     currentChunks,
			}

			lastPlaylistID = newPlaylistID
			currentChunks = nil
			bytesInCurrentPlaylist = 0
			playlistCount++
		}

		if err == io.EOF {
			break
		}
	}

	close(jobs)
	fmt.Println("All playlist links created. Finishing track uploads...")
	wg.Wait()
	fmt.Println("All songs were added to the linked playlists successfully.")
}

func ReaderWorker(ctx context.Context, s *spotify.SpotifyClient, jobs <-chan ReadJob, results chan<- ReadResult, readerdictionary map[string]byte) {
	for j := range jobs {
		playlistURL := fmt.Sprintf(s.WebConfig.PlaylistURL, j.PlaylistID)
		var allBytes []byte
		var nextPlaylistID string

		rateLimitMultiplier := 1

		for {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, playlistURL, nil)
			req.Header.Set("Authorization", "Bearer "+s.Auth.Token.AccessToken)

			query := req.URL.Query()
			query.Add("fields", "next,items(track(uri))")
			query.Add("limit", "50")
			query.Add("market", "US")
			req.URL.RawQuery = query.Encode()

			resp, err := s.WebConfig.Client.Do(req)
			if err != nil {
				log.Printf("Request Error: %v. Trying Again", err)
				continue
			}

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				if resp.StatusCode == 429 {
					retryAfterStr := resp.Header.Get("Retry-After")
					waitTime := spotify.RateLimitWaitTime

					if retryAfterStr != "" {
						if seconds, err := strconv.Atoi(retryAfterStr); err == nil {
							waitTime = seconds + 1
						}
					}

					jitter := mathRand.IntN(1000)
					log.Printf("[Worker] Rate limit (429). Waiting %d seconds + %d ms of jitter...", waitTime, jitter)

					time.Sleep(time.Duration(waitTime)*time.Second + time.Duration(jitter)*time.Millisecond)

					continue

				}

				if resp.StatusCode == 502 {
					log.Printf("[Worker] Error 502 in playlist %s. Trying again in 1s...", j.PlaylistID)
					time.Sleep(1 * time.Second)
					rateLimitMultiplier++
					resp.Body.Close()
					continue
				}

				log.Printf("Fatal error %d playlist %s", resp.StatusCode, j.PlaylistID)
				resp.Body.Close()
				break
			}

			var items spotify.PlaylistItems
			json.NewDecoder(resp.Body).Decode(&items)
			resp.Body.Close()

			for _, item := range items.Items {
				if b, ok := readerdictionary[item.Track.Uri]; ok {
					allBytes = append(allBytes, b)
				} else {
					log.Fatal("Stopping execution to avoid saving a corrupted file.")
				}
			}

			if items.Next == "" {
				nextID, _ := s.GetNextPlaylist(ctx, j.PlaylistID)
				nextPlaylistID = nextID
				break
			}
			playlistURL = items.Next
		}

		results <- ReadResult{
			Sequence: j.Sequence,
			Data:     allBytes,
			NextID:   nextPlaylistID,
		}
	}
}

func Reader(startPlaylistID, filename, password, decoder string, s *spotify.SpotifyClient) {
	ctx := context.Background()
	var readerdictionary map[string]byte
	var err error

	if decoder == "" {
		_, readerdictionary, err = crypto.NewDictionary(ctx, password, s)
	} else {
		readerdictionary, err = crypto.LoadMap(decoder, password)
	}
	if err != nil {
		log.Fatal(err)
	}

	jobs := make(chan ReadJob, numWorkers)
	results := make(chan ReadResult, numWorkers)

	for w := 0; w < numWorkers; w++ {
		go ReaderWorker(ctx, s, jobs, results, readerdictionary)
	}

	f, _ := os.OpenFile(filename, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0644)
	defer f.Close()

	pendingResults := make(map[int]ReadResult)
	nextToWrite := 0
	currentPlaylistID := startPlaylistID
	jobsSent := 0

	doneSending := false

	for {
		if !doneSending && len(jobs) < numWorkers && currentPlaylistID != "" {
			jobs <- ReadJob{Sequence: jobsSent, PlaylistID: currentPlaylistID}

			currentPlaylistID, err = s.GetNextPlaylist(ctx, currentPlaylistID)
			jobsSent++
			if currentPlaylistID == "" && errors.Is(err, spotify.ErrNoMorePlaylist) {
				doneSending = true
			} else if err != nil {
				fmt.Printf("Error while getting next playlist: %v\n", err)
				return
			}
		}

		select {
		case res := <-results:
			pendingResults[res.Sequence] = res
			for {
				if nextRes, ok := pendingResults[nextToWrite]; ok {
					f.Write(nextRes.Data)
					fmt.Printf("Playlist sequence %d written to the file.\n", nextToWrite)
					delete(pendingResults, nextToWrite)
					nextToWrite++

					if doneSending && nextToWrite == jobsSent {
						fmt.Println("Completed!")
						return
					}
				} else {
					break
				}
			}
		case <-time.After(time.Second * 10):
			if doneSending && nextToWrite == jobsSent {
				return
			}
		}
	}
}
