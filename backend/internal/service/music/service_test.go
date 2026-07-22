package music

import (
	"errors"
	"mime/multipart"
	"os"
	"path/filepath"
	"testing"

	"mongojson/backend/internal/domain"
)

func TestValidateTrackFilesMarksMissingAudioAsDeletableIssue(t *testing.T) {
	record := validateTrackFiles(domain.MusicTrackRecord{StoragePath: filepath.Join(t.TempDir(), "missing.mp3")})

	if record.FileAvailable {
		t.Fatal("expected missing audio to be unavailable")
	}
	if record.RecordIssue != "音频文件缺失，建议删除此歌曲记录" {
		t.Fatalf("unexpected issue: %q", record.RecordIssue)
	}
}

func TestValidateArtworkAcceptsSupportedImageSignatures(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected string
	}{
		{name: "jpeg", data: []byte{0xff, 0xd8, 0xff, 0xe0, 0, 0x10, 'J', 'F', 'I', 'F', 0}, expected: "image/jpeg"},
		{name: "png", data: []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, expected: "image/png"},
		{name: "webp", data: []byte{'R', 'I', 'F', 'F', 0x0c, 0, 0, 0, 'W', 'E', 'B', 'P', 'V', 'P', '8', ' '}, expected: "image/webp"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "artwork-*")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if _, err := file.Write(test.data); err != nil {
				t.Fatal(err)
			}
			if _, err := file.Seek(0, 0); err != nil {
				t.Fatal(err)
			}

			got, err := validateArtwork(file, &multipart.FileHeader{Filename: "cover." + test.name, Size: int64(len(test.data))})
			if err != nil {
				t.Fatalf("validate artwork: %v", err)
			}
			if got != test.expected {
				t.Fatalf("expected %q, got %q", test.expected, got)
			}
			position, err := file.Seek(0, 1)
			if err != nil || position != 0 {
				t.Fatalf("expected artwork reader to rewind, position=%d err=%v", position, err)
			}
		})
	}
}

func TestValidateArtworkRejectsUnsupportedAndOversizedFiles(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "artwork-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString("not-an-image"); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}

	if _, err := validateArtwork(file, &multipart.FileHeader{Filename: "cover.txt", Size: 12}); !errors.Is(err, ErrUnsupportedArtwork) {
		t.Fatalf("expected unsupported artwork error, got %v", err)
	}
	if _, err := validateArtwork(file, &multipart.FileHeader{Filename: "cover.png", Size: maxArtworkBytes + 1}); !errors.Is(err, ErrArtworkTooLarge) {
		t.Fatalf("expected artwork too large error, got %v", err)
	}
}

func TestValidateTrackFilesKeepsAudioPlayableWhenOnlyLyricsAreMissing(t *testing.T) {
	dir := t.TempDir()
	audioPath := filepath.Join(dir, "song.mp3")
	if err := os.WriteFile(audioPath, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	record := validateTrackFiles(domain.MusicTrackRecord{
		StoragePath:      audioPath,
		LyricStoragePath: filepath.Join(dir, "missing.lrc"),
	})

	if !record.FileAvailable {
		t.Fatal("expected audio to remain available")
	}
	if record.RecordIssue != "歌词文件缺失，歌曲仍可播放" {
		t.Fatalf("unexpected issue: %q", record.RecordIssue)
	}
}

func TestValidateTrackFilesMarksNewlyStoredAudioAsAvailable(t *testing.T) {
	dir := t.TempDir()
	audioPath := filepath.Join(dir, "uploaded.mp3")
	if err := os.WriteFile(audioPath, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}

	record := validateTrackFiles(domain.MusicTrackRecord{StoragePath: audioPath})

	if !record.FileAvailable {
		t.Fatal("expected newly stored audio to be immediately available")
	}
	if record.RecordIssue != "" {
		t.Fatalf("unexpected issue: %q", record.RecordIssue)
	}
}

func TestValidateTrackFilesMarksStoredArtworkAsAvailable(t *testing.T) {
	dir := t.TempDir()
	audioPath := filepath.Join(dir, "uploaded.mp3")
	artworkPath := filepath.Join(dir, "cover.jpg")
	if err := os.WriteFile(audioPath, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artworkPath, []byte("artwork"), 0o600); err != nil {
		t.Fatal(err)
	}

	record := validateTrackFiles(domain.MusicTrackRecord{StoragePath: audioPath, ArtworkStoragePath: artworkPath})

	if !record.ArtworkAvailable {
		t.Fatal("expected stored artwork to be available")
	}
}
