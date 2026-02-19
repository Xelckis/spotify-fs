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
	"unlimitedfs/pkg/crypto"
	"unlimitedfs/pkg/youtube"
	"strconv"
	"sync"
	"time"
)

const (
	maxVideosPerPlaylist = 5000
)

func YouTubeWriterWorker(ctx context.Context, s *youtube.YouTubeClient, job <-chan WriteJob, writerdictionary map[byte]string, wg *sync.WaitGroup) {
	defer wg.Done()
	for j := range job {
		for i, chunk := range j.Chunks {
			for _, b := range chunk {
				videoID := writerdictionary[b]

				for {
					err := s.AddToPlaylist(ctx, videoID, j.PlaylistID)
					if err == nil {
						break
					}

					log.Printf("[Worker] Error adding video to playlist %s (chunk %d): %v", j.PlaylistID, i, err)
					time.Sleep(1 * time.Second)
				}
			}
		}
		fmt.Printf("Successfully finished all chunks for playlist %s\n", j.PlaylistID)
	}
}

func YouTubeWriter(s *youtube.YouTubeClient, filepath string, password string, playlistName string) {
	ctx := context.Background()
	writerdictionary, readerdictionary, err := crypto.NewYouTubeDictionary(ctx, password, s)
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
		go YouTubeWriterWorker(ctx, s, jobs, writerdictionary, &wg)
	}

	playlistCount := 0
	lastPlaylistID := ""
	var currentChunks [][]byte
	videosInCurrentPlaylist := 0
	readBuf := make([]byte, youtube.YouTubeChunkSize)

	for {
		n, err := file.Read(readBuf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, readBuf[:n])
			currentChunks = append(currentChunks, chunk)
			videosInCurrentPlaylist += n
		}

		if (videosInCurrentPlaylist >= maxVideosPerPlaylist || err == io.EOF) && len(currentChunks) > 0 {
			newPlaylistID, createErr := s.CreatePlaylist(ctx, playlistName, lastPlaylistID, playlistCount)
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
			videosInCurrentPlaylist = 0
			playlistCount++
		}

		if err == io.EOF {
			break
		}
	}

	close(jobs)
	fmt.Println("All playlist links created. Finishing video uploads...")
	wg.Wait()

	if lastPlaylistID != "" {
		err := s.EditPlaylistDescription(ctx, "null", lastPlaylistID)
		if err != nil {
			log.Printf("Warning: could not set last playlist description to null: %v", err)
		}
	}

	fmt.Println("All videos were added to the linked playlists successfully.")
}

func YouTubeReaderWorker(ctx context.Context, s *youtube.YouTubeClient, jobs <-chan ReadJob, results chan<- ReadResult, readerdictionary map[string]byte) {
	for j := range jobs {
		var allBytes []byte
		var nextPlaylistID string

		pageToken := ""

		for {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.WebConfig.PlaylistItemsURL, nil)
			req.Header.Set("Authorization", "Bearer "+s.Auth.Token.AccessToken)

			query := req.URL.Query()
			query.Add("part", "snippet")
			query.Add("playlistId", j.PlaylistID)
			query.Add("maxResults", "50")
			if pageToken != "" {
				query.Add("pageToken", pageToken)
			}
			req.URL.RawQuery = query.Encode()

			resp, err := s.WebConfig.Client.Do(req)
			if err != nil {
				log.Printf("Request Error: %v. Trying Again", err)
				continue
			}

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				if resp.StatusCode == 429 || resp.StatusCode == 403 {
					retryAfterStr := resp.Header.Get("Retry-After")
					waitTime := youtube.RateLimitWaitTime

					if retryAfterStr != "" {
						if seconds, err := strconv.Atoi(retryAfterStr); err == nil {
							waitTime = seconds + 1
						}
					}

					jitter := mathRand.IntN(1000)
					log.Printf("[Worker] Rate limit (%d). Waiting %d seconds + %d ms of jitter...", resp.StatusCode, waitTime, jitter)
					time.Sleep(time.Duration(waitTime)*time.Second + time.Duration(jitter)*time.Millisecond)
					resp.Body.Close()
					continue
				}

				if resp.StatusCode == 502 || resp.StatusCode == 503 {
					log.Printf("[Worker] Error %d in playlist %s. Trying again in 1s...", resp.StatusCode, j.PlaylistID)
					time.Sleep(1 * time.Second)
					resp.Body.Close()
					continue
				}

				log.Printf("Fatal error %d playlist %s", resp.StatusCode, j.PlaylistID)
				resp.Body.Close()
				break
			}

			var items youtube.PlaylistItemListResponse
			json.NewDecoder(resp.Body).Decode(&items)
			resp.Body.Close()

			for _, item := range items.Items {
				videoID := item.Snippet.ResourceID.VideoID
				if b, ok := readerdictionary[videoID]; ok {
					allBytes = append(allBytes, b)
				} else {
					log.Fatal("Stopping execution to avoid saving a corrupted file.")
				}
			}

			if items.NextPageToken == "" {
				nextID, _ := s.GetNextPlaylist(ctx, j.PlaylistID)
				nextPlaylistID = nextID
				break
			}
			pageToken = items.NextPageToken
		}

		results <- ReadResult{
			Sequence: j.Sequence,
			Data:     allBytes,
			NextID:   nextPlaylistID,
		}
	}
}

func YouTubeReader(startPlaylistID, filename, password, decoder string, s *youtube.YouTubeClient) {
	ctx := context.Background()
	var readerdictionary map[string]byte
	var err error

	if decoder == "" {
		_, readerdictionary, err = crypto.NewYouTubeDictionary(ctx, password, s)
	} else {
		readerdictionary, err = crypto.LoadMap(decoder, password)
	}
	if err != nil {
		log.Fatal(err)
	}

	jobs := make(chan ReadJob, numWorkers)
	results := make(chan ReadResult, numWorkers)

	for w := 0; w < numWorkers; w++ {
		go YouTubeReaderWorker(ctx, s, jobs, results, readerdictionary)
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
			if currentPlaylistID == "" && errors.Is(err, youtube.ErrNoMorePlaylist) {
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
