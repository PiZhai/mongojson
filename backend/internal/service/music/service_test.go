package music

import (
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
