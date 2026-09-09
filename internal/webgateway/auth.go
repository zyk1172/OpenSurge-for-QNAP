package webgateway

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

var (
	ErrSetupRequired = errors.New("administrator account is not configured")
	ErrAlreadySetup  = errors.New("administrator account is already configured")
	ErrInvalidLogin  = errors.New("invalid username or password")
)

const (
	authSchemaVersion = 1
	argonTime         = 3
	argonMemory       = 64 * 1024
	argonThreads      = 2
	argonKeyLen       = 32
)

type credentialFile struct {
	SchemaVersion int       `json:"schema_version"`
	Username      string    `json:"username"`
	Salt          string    `json:"salt"`
	Hash          string    `json:"hash"`
	ArgonTime     uint32    `json:"argon_time"`
	ArgonMemory   uint32    `json:"argon_memory_kib"`
	ArgonThreads  uint8     `json:"argon_threads"`
	ArgonKeyLen   uint32    `json:"argon_key_len"`
	CreatedAt     time.Time `json:"created_at"`
}

type AuthStore struct {
	path string
	mu   sync.Mutex
}

func NewAuthStore(dir string) *AuthStore {
	return &AuthStore{path: filepath.Join(dir, "admin.json")}
}

func (s *AuthStore) SetupRequired() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := os.Stat(s.path)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	return false, err
}

func (s *AuthStore) Setup(username, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.path); err == nil {
		return ErrAlreadySetup
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	username = strings.TrimSpace(username)
	if len(username) < 3 || len(username) > 64 {
		return fmt.Errorf("username must contain 3 to 64 characters")
	}
	if strings.ContainsAny(username, "\r\n\t") {
		return fmt.Errorf("username contains unsupported control characters")
	}
	if len(password) < 12 || len(password) > 1024 {
		return fmt.Errorf("password must contain 12 to 1024 characters")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	credential := credentialFile{
		SchemaVersion: authSchemaVersion,
		Username:      username,
		Salt:          base64.RawStdEncoding.EncodeToString(salt),
		Hash:          base64.RawStdEncoding.EncodeToString(hash),
		ArgonTime:     argonTime,
		ArgonMemory:   argonMemory,
		ArgonThreads:  argonThreads,
		ArgonKeyLen:   argonKeyLen,
		CreatedAt:     time.Now().UTC(),
	}
	payload, err := json.MarshalIndent(credential, "", "  ")
	if err != nil {
		return err
	}
	return writeDurableAtomic(s.path, append(payload, '\n'), 0o600)
}

func (s *AuthStore) Verify(username, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	credential, err := s.loadLocked()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrSetupRequired
		}
		return err
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(username)), []byte(credential.Username)) != 1 {
		// Still perform the expensive KDF below using the stored parameters so a
		// username probe does not get a cheap timing oracle.
		username = credential.Username
	}
	salt, err := base64.RawStdEncoding.DecodeString(credential.Salt)
	if err != nil {
		return fmt.Errorf("decode administrator salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(credential.Hash)
	if err != nil {
		return fmt.Errorf("decode administrator hash: %w", err)
	}
	got := argon2.IDKey([]byte(password), salt, credential.ArgonTime, credential.ArgonMemory, credential.ArgonThreads, credential.ArgonKeyLen)
	userOK := subtle.ConstantTimeCompare([]byte(strings.TrimSpace(username)), []byte(credential.Username)) == 1
	hashOK := subtle.ConstantTimeCompare(got, want) == 1
	if !userOK || !hashOK {
		return ErrInvalidLogin
	}
	return nil
}

func (s *AuthStore) loadLocked() (credentialFile, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return credentialFile{}, err
	}
	var credential credentialFile
	if err := json.Unmarshal(data, &credential); err != nil {
		return credentialFile{}, fmt.Errorf("decode administrator credential: %w", err)
	}
	if credential.SchemaVersion != authSchemaVersion || credential.Username == "" || credential.Salt == "" || credential.Hash == "" {
		return credentialFile{}, fmt.Errorf("administrator credential file is invalid or unsupported")
	}
	if credential.ArgonTime == 0 || credential.ArgonMemory < 8*1024 || credential.ArgonThreads == 0 || credential.ArgonKeyLen < 16 {
		return credentialFile{}, fmt.Errorf("administrator credential parameters are invalid")
	}
	return credential, nil
}

func writeDurableAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".auth-*.tmp")
	if err != nil {
		return err
	}
	tmp := file.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	cleanup = false
	dirFile, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer dirFile.Close()
	return dirFile.Sync()
}
