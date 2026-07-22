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
	// root 指向 .context/sessions；每个 Session 都拥有独立子目录。
	root string
	// mu 保护同一进程内的追加写和 manifest 切换，避免文件内容互相覆盖。
	mu sync.Mutex
}

// NewFileConversationStore 创建基于本地文件的 ConversationStore。
// 目录权限固定为 0700，防止同一机器上的其他用户直接读取会话内容。
func NewFileConversationStore(root string) (*FileConversationStore, error) {
	if root == "" {
		return nil, errors.New("context store root is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create context store: %w", err)
	}
	return &FileConversationStore{root: root}, nil
}

// AppendMessage 将原始消息追加到 transcript.jsonl。
// transcript 是审计事实来源；后续的卸载、淘汰和压缩都不会回写或删除这里的内容。
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
	// 每条消息独占一行，既便于顺序追加，也便于未来按事件流进行恢复。
	data = append(data, '\n')
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("append transcript: %w", err)
	}
	return file.Sync()
}

// ListMessages 按写入顺序读取一个 Session 的全部原始消息。
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

// ListMessagesAfter 只返回压缩检查点之后的消息。
// 如果检查点不存在则返回错误，防止错误游标导致旧消息被静默跳过。
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

// SaveToolArtifact 将完整工具输出和元数据分别保存为 .txt 与 .json。
// 正文先写临时文件、计算 SHA256，再原子重命名；只有归档成功后上层才能用引用替换正文。
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
	// io.MultiWriter 保证写盘内容和参与哈希计算的字节完全一致。
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

// LoadToolArtifact 读取完整工具结果，并在返回前验证 SHA256。
// 校验失败时不返回正文，避免模型使用已经损坏或遭到替换的内容。
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

// ActiveSummary 只读取 manifest 指向的摘要。
// summaries 目录中即使存在更高版本文件，只要没有被 manifest 激活就一律忽略。
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

// CommitSummary 以乐观锁方式提交摘要和覆盖游标。
// expectedVersion 必须等于当前 active version，防止较旧的压缩任务覆盖较新的检查点。
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
	// 先持久化摘要正文和元数据。此阶段崩溃最多留下未激活的孤立文件，
	// 下次 Build 仍会继续使用旧 manifest，因此不会丢失上下文。
	if err := writeFileAtomic(base+".md", []byte(summary.Content)); err != nil {
		return err
	}
	if err := writeJSONAtomic(base+".json", summary); err != nil {
		return err
	}
	// 最后切换 manifest。只有这一步成功后，新摘要及其覆盖游标才同时生效。
	manifest.ActiveSummaryVersion = summary.Version
	manifest.CoveredThroughMessageID = summary.CoveredThroughMessageID
	manifest.CoveredThroughTurnID = summary.CoveredThroughTurnID
	manifest.UpdatedAt = time.Now()
	return writeJSONAtomic(filepath.Join(dir, "manifest.json"), manifest)
}

// ensureSession 创建 Session 目录和初始 manifest。
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

// validIdentifier 限制可进入本地路径的标识符字符集，阻止 ../ 等路径穿越形式。
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

// writeFileAtomic 通过“同目录临时文件 + fsync + rename”提交单个文件。
// 临时文件与目标文件位于同一文件系统，rename 才能提供可靠的原子替换语义。
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
