package crypto

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	youtube "unlimitedfs/pkg/youtube"
)

func NewYouTubeDictionary(ctx context.Context, password string, s *youtube.YouTubeClient) (map[byte]string, map[string]byte, error) {
	h := sha256.New()
	h.Write([]byte(password))
	hash := h.Sum(nil)

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

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.WebConfig.SearchURL, nil)
			if err != nil {
				log.Printf("Error creating the request: %s\n Trying Again...", err)
				return nil
			}

			req.Header.Set("Authorization", "Bearer "+s.Auth.Token.AccessToken)

			query := req.URL.Query()
			query.Add("part", "id")
			query.Add("q", searchString)
			query.Add("type", "video")
			query.Add("maxResults", "50")

			req.URL.RawQuery = query.Encode()

			resp, err := s.WebConfig.Client.Do(req)
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

			var response youtube.YouTubeSearchResponse
			err = json.NewDecoder(resp.Body).Decode(&response)
			if err != nil {
				log.Printf("Error reading JSON: %v\n Trying Again...", err)
				return nil
			}

			for _, item := range response.Items {
				videoID := item.ID.VideoID
				if videoID == "" {
					continue
				}

				if _, alreadyExists := readerDictionary[videoID]; alreadyExists {
					log.Printf("Collision detected for video %s. Skipping...", videoID)
					continue
				}

				writerDictionary[byteCount] = videoID
				readerDictionary[videoID] = byteCount
				byteCount++
				foundCount++

				if foundCount > 255 {
					break
				}
			}

			return nil
		}()
		log.Printf("Videos %d/256\n", foundCount)

		seedDiff++

		if err != nil {
			return nil, nil, err
		}
	}

	return writerDictionary, readerDictionary, nil
}
