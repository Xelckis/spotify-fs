![Spotify-fs](header.png)

**spotify-fs** is a Proof of Concept (PoC) tool written in Go that allows you to store arbitrary files inside Spotify playlists. 

It works by transforming binary data into a sequence of Spotify tracks. Essentially, it maps byte values (0-255) to specific songs and arranges them in a playlist to represent the file.

> ⚠️ **DISCLAIMER:** This project is for educational and research purposes only. Storing data in playlists likely violates Spotify's Terms of Service. The author is not responsible for banned accounts or data loss. Use at your own risk.

## 🚀 Features

- **Encrypted/Seeded Mapping:** Uses a password to generate a unique dictionary mapping bytes to tracks. Without the password (and the generated decoder map), the playlist just looks like a random collection of songs.
- **Chunking & Chaining:** Automatically splits large files across multiple playlists if they exceed the track limit. Playlists are linked together via their description fields.
- **Concurrency:** Uses multiple workers to speed up the writing (adding tracks) and reading (fetching tracks) processes.
- **Rate Limit Handling:** Automatically backs off and retries when hitting Spotify API rate limits (429) or gateway errors (502).

## 🛠️ Prerequisites

- **Go**: Version 1.25 or higher.
- **Spotify Account**: Required for API access to modify playlists effectively.
- **Spotify Developer Application**: You need a Client ID and Client Secret.

## ⚙️ Setup

### 1. **Clone the repository:**
   ```bash
   git clone [https://github.com/xelckis/spotify-fs.git](https://github.com/xelckis/spotify-fs.git)
   cd spotify-fs
   ```

### 2. Create a Spotify App:
   - Go to the Spotify Developer Dashboard.
   - Create an app and set the Redirect URI to: http://127.0.0.1:8080/callback/spotify

### 3. Set Environment Variables: You must export your credentials before running the tool:

Linux/macOS:
```bash
export SPOTIFY_CLIENTID="your_client_id_here"
export SPOTIFY_CLIENTSECRET="your_client_secret_here"
```
Windows (PowerShell):
```PowerShell
$env:SPOTIFY_CLIENTID="your_client_id_here"
$env:SPOTIFY_CLIENTSECRET="your_client_secret_here"
```
## 📦 Usage

Run the application:
```bash
go run main.go
```
Follow the on-screen interactive prompts.

### 1. Writing a File (Upload)

Select option 1.

  1. Filepath: Path to the file you want to upload.

  2. Playlist Name: The base name for the playlist(s).

  3. Password: Used to seed the random generation of the byte-to-track dictionary.

   The tool will:

  - Authenticate via your browser.

  - Create a [PlaylistName]_Decoder.gob file locally (keep this safe! It helps speed up reading).

  - Upload the data to Spotify.

### 2. Reading a File (Download)

Select option 2.

  1. Playlist ID: The ID of the first playlist in the chain (found in the Spotify URL).

  2. Output Filename: Name (including extension) to save the restored file.

  3. Decoder Path (Optional): Path to the _Decoder.gob file generated during upload. If skipped, the tool attempts to regenerate the map using the password (slower).

  4. Password: Must match the one used during upload.

## 🔧 Technical Details

  - Dictionary Generation: The tool searches Spotify for random tracks based on a seed derived from your password. It assigns a unique Track URI to every byte value (0x00 to 0xFF).

  - Storage: The file is read in chunks. Each byte is converted to its corresponding Track URI and added to a playlist.

  - Linked List: If a file is too large for one playlist, a new one is created. The ID of the next playlist is stored in the description of the current playlist, forming a linked list.
