package crypto

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	mathRand "math/rand/v2"
	"net/http"
	"os"
	spotify "spotifyfs/pkg/spotify"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const (
	Charset         = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	LengthRNGString = 5
	saltSize        = 16
	keySize         = 32
	pbkdfIterations = 100000
)

func SaveMap(path string, m map[string]byte, password string) error {
	var gobBuffer bytes.Buffer
	if err := gob.NewEncoder(&gobBuffer).Encode(m); err != nil {
		return err
	}
	plaintext := gobBuffer.Bytes()

	salt := make([]byte, saltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return err
	}

	key := pbkdf2.Key([]byte(password), salt, pbkdfIterations, keySize, sha256.New)

	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	io.ReadFull(rand.Reader, nonce)

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	file.Write(salt)
	file.Write(nonce)
	file.Write(ciphertext)

	return nil

}

func LoadMap(path, password string) (map[string]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(make([]byte, keySize))
	if err != nil {
		return nil, err
	}
	gcmTemp, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcmTemp.NonceSize()

	if len(data) < saltSize+nonceSize {
		return nil, errors.New("corrupted or too short file")
	}

	salt := data[:saltSize]
	nonce := data[saltSize : saltSize+nonceSize]
	ciphertext := data[saltSize+nonceSize:]

	key := pbkdf2.Key([]byte(password), salt, pbkdfIterations, keySize, sha256.New)

	block, err = aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("Decryption failed: incorrect password or altered data.")
	}

	var result map[string]byte
	reader := bytes.NewReader(plaintext)
	err = gob.NewDecoder(reader).Decode(&result)

	return result, err
}

func NewRNGStringWithSeed(length int, hash []byte, modifier uint64) string {
	baseSeed := binary.BigEndian.Uint64(hash)

	seed := baseSeed + modifier

	source := mathRand.NewPCG(seed, 0)
	r := mathRand.New(source)

	var sb strings.Builder
	sb.Grow(length)

	for i := 0; i < length; i++ {
		randomIndex := r.IntN(len(Charset))
		sb.WriteByte(Charset[randomIndex])
	}
	return sb.String()
}

func NewDictionary(ctx context.Context, password string, s *spotify.SpotifyClient) (map[byte]string, map[string]byte, error) {
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

			var response spotify.SpotifySearchResponse
			err = json.NewDecoder(resp.Body).Decode(&response)
			if err != nil {
				log.Printf("Error reading JSON: %v\n Trying Again...", err)
				return nil
			}

			if len(response.Tracks.Items) > 0 {

				if _, alreadyExists := readerDictionary[response.Tracks.Items[0].URI]; alreadyExists {
					log.Printf("Collision detected for track %s. Trying another one...", response.Tracks.Items[0].URI)
					return nil
				}

				writerDictionary[byteCount] = response.Tracks.Items[0].URI
				readerDictionary[response.Tracks.Items[0].URI] = byteCount
				byteCount++
				foundCount++
			}

			return nil
		}()
		log.Printf("Track %d/256\n", foundCount)

		seedDiff++

		if err != nil {
			return nil, nil, err
		}
	}

	return writerDictionary, readerDictionary, nil
}
