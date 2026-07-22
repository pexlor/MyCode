package contextmanager

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

const storeFormatVersion = 1

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type FileConversationStore struct {
	root string
	mu   sync.Mutex
}

func NewFileConversationStore(root string) (*FileConversationStore, error) {
	if root == "" {
		return nil, errors.New("context store root is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create context store: %w", err)
	}
	return &FileConversationStore{root: root}, nil
}

func (s *FileConversationStore) AppendMessage(ctx context.Context, message StoredMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validIdentifier(message.SessionID) || !validIdentifier(message.ID) {
		return ErrInvalidIdentifier
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.ensureSession(message.SessionID)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(dir, "transcript.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open transcript: %w", err)
	}
	defer file.Close()
	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}
	data = append(data, '\n')
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("append transcript: %w", err)
	}
	return file.Sync()
}

func (s *FileConversationStore) ListMessages(ctx context.Context, sessionID string) ([]StoredMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validIdentifier(sessionID) {
		return nil, ErrInvalidIdentifier
	}
	path := filepath.Join(s.root, sessionID, "transcript.jsonl")
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer file.Close()
	var messages []StoredMessage
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var message StoredMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			return nil, fmt.Errorf("decode transcript: %w", err)
		}
		messages = append(messages, message)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read transcript: %w", err)
	}
	return messages, nil
}

func (s *FileConversationStore) ListMessagesAfter(ctx context.Context, sessionID, messageID string) ([]StoredMessage, error) {
	messages, err := s.ListMessages(ctx, sessionID)
	if err != nil || messageID == "" {
		return messages, err
	}
	for index, message := range messages {
		if message.ID == messageID {
			return append([]StoredMessage(nil), messages[index+1:]...), nil
		}
	}
	return nil, fmt.Errorf("checkpoint message %q not found", messageID)
}

func (s *FileConversationStore) SaveToolArtifact(ctx context.Context, artifact ToolArtifact, content io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validIdentifier(artifact.SessionID) || !validIdentifier(artifact.ID) {
		return ErrInvalidIdentifier
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.ensureSession(artifact.SessionID)
	if err != nil {
		return err
	}
	artifactDir := filepath.Join(dir, "tool-results")
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}
	bodyPath := filepath.Join(artifactDir, artifact.ID+".txt")
	temporary, err := os.CreateTemp(artifactDir, artifact.ID+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create artifact: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hash), content)
	closeErr := temporary.Close()
	if copyErr != nil {
		return fmt.Errorf("write artifact: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close artifact: %w", closeErr)
	}
	if err := os.Rename(temporaryName, bodyPath); err != nil {
		return fmt.Errorf("commit artifact: %w", err)
	}
	artifact.ByteSize = written
	artifact.ContentSHA256 = hex.EncodeToString(hash.Sum(nil))
	artifact.StoragePath = bodyPath
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now()
	}
	return writeJSONAtomic(filepath.Join(artifactDir, artifact.ID+".json"), artifact)
}

func (s *FileConversationStore) LoadToolArtifact(ctx context.Context, sessionID, artifactID string) (ToolArtifact, io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return ToolArtifact{}, nil, err
	}
	if !validIdentifier(sessionID) || !validIdentifier(artifactID) {
		return ToolArtifact{}, nil, ErrInvalidIdentifier
	}
	dir := filepath.Join(s.root, sessionID, "tool-results")
	var artifact ToolArtifact
	if err := readJSON(filepath.Join(dir, artifactID+".json"), &artifact); err != nil {
		return ToolArtifact{}, nil, err
	}
	file, err := os.Open(filepath.Join(dir, artifactID+".txt"))
	if err != nil {
		return ToolArtifact{}, nil, err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		file.Close()
		return ToolArtifact{}, nil, err
	}
	if hex.EncodeToString(hash.Sum(nil)) != artifact.ContentSHA256 {
		file.Close()
		return ToolArtifact{}, nil, ErrArtifactHashMismatch
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return ToolArtifact{}, nil, err
	}
	return artifact, file, nil
}

func (s *FileConversationStore) ActiveSummary(ctx context.Context, sessionID string) (*SummarySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validIdentifier(sessionID) {
		return nil, ErrInvalidIdentifier
	}
	var manifest sessionManifest
	if err := readJSON(filepath.Join(s.root, sessionID, "manifest.json"), &manifest); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if manifest.ActiveSummaryVersion == 0 {
		return nil, nil
	}
	var summary SummarySnapshot
	path := filepath.Join(s.root, sessionID, "summaries", fmt.Sprintf("summary-%04d.json", manifest.ActiveSummaryVersion))
	if err := readJSON(path, &summary); err != nil {
		return nil, err
	}
	return &summary, nil
}

func (s *FileConversationStore) CommitSummary(ctx context.Context, summary SummarySnapshot, expectedVersion int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validIdentifier(summary.SessionID) || summary.Version <= 0 || summary.Content == "" {
		return ErrInvalidIdentifier
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.ensureSession(summary.SessionID)
	if err != nil {
		return err
	}
	manifest, err := loadManifest(filepath.Join(dir, "manifest.json"), summary.SessionID)
	if err != nil {
		return err
	}
	if manifest.ActiveSummaryVersion != expectedVersion {
		return ErrSummaryVersionConflict
	}
	if summary.Version != expectedVersion+1 {
		return ErrSummaryVersionConflict
	}
	if summary.CreatedAt.IsZero() {
		summary.CreatedAt = time.Now()
	}
	summary.PreviousSummaryVersion = expectedVersion
	summaryDir := filepath.Join(dir, "summaries")
	if err := os.MkdirAll(summaryDir, 0o700); err != nil {
		return err
	}
	base := filepath.Join(summaryDir, fmt.Sprintf("summary-%04d", summary.Version))
	if err := writeFileAtomic(base+".md", []byte(summary.Content)); err != nil {
		return err
	}
	if err := writeJSONAtomic(base+".json", summary); err != nil {
		return err
	}
	manifest.ActiveSummaryVersion = summary.Version
	manifest.CoveredThroughMessageID = summary.CoveredThroughMessageID
	manifest.CoveredThroughTurnID = summary.CoveredThroughTurnID
	manifest.UpdatedAt = time.Now()
	return writeJSONAtomic(filepath.Join(dir, "manifest.json"), manifest)
}

func (s *FileConversationStore) ensureSession(sessionID string) (string, error) {
	if !validIdentifier(sessionID) {
		return "", ErrInvalidIdentifier
	}
	dir := filepath.Join(s.root, sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	if _, err := os.Stat(manifestPath); errors.Is(err, os.ErrNotExist) {
		now := time.Now()
		manifest := sessionManifest{FormatVersion: storeFormatVersion, SessionID: sessionID, CreatedAt: now, UpdatedAt: now}
		if err := writeJSONAtomic(manifestPath, manifest); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	return dir, nil
}

func validIdentifier(value string) bool {
	return value != "" && identifierPattern.MatchString(value)
}

func loadManifest(path, sessionID string) (sessionManifest, error) {
	var manifest sessionManifest
	if err := readJSON(path, &manifest); err != nil {
		return sessionManifest{}, err
	}
	if manifest.FormatVersion != storeFormatVersion || manifest.SessionID != sessionID {
		return sessionManifest{}, errors.New("unsupported or mismatched context manifest")
	}
	return manifest, nil
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'))
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, path)
}
