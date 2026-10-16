package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// FileStore 抽象：本地/MinIO/云 OSS 统一接口（production-delivery-blueprint §2d）。
type FileStore interface {
	// Put 存储文件，key 为完整存储键（如 inbox/{projectID}/{yyyymm}/{uuid7}-{origName}）。
	Put(ctx context.Context, key string, r io.Reader, size int64, mime string) error
	// Open 以只读流打开文件；调用方需 Close。
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete 删除文件（幂等：不存在也不报错）。
	Delete(ctx context.Context, key string) error
	// Presign 生成短 TTL 预签名 URL（≤5min，仅大文件场景）。
	Presign(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// Config 环境变量装配（production-delivery-blueprint §2d 表）。
type Config struct {
	Driver    string // local | s3
	LocalRoot string // LOCAL_ROOT，默认 /app/uploads

	// S3 / MinIO
	S3Endpoint  string
	S3Bucket    string
	S3Region    string
	S3AccessKey string
	S3Secret    string
	S3PathStyle bool
}

// ConfigFromEnv 从环境变量读取配置。
func ConfigFromEnv() Config {
	cfg := Config{
		Driver:    strings.ToLower(strings.TrimSpace(os.Getenv("FORJA_STORAGE_DRIVER"))),
		LocalRoot: strings.TrimSpace(os.Getenv("LOCAL_ROOT")),
	}
	if cfg.Driver == "" {
		cfg.Driver = "local"
	}
	if cfg.LocalRoot == "" {
		cfg.LocalRoot = "/app/uploads"
		// Windows 本地开发宿主路径（deploy/compose 绝对路径挂载要求见 AGENTS.md #12）
		if os.Getenv("FORJA_STORAGE_LOCAL_HOST_PATH") != "" {
			cfg.LocalRoot = os.Getenv("FORJA_STORAGE_LOCAL_HOST_PATH")
		} else if isWindows() {
			// 本地 go test / go run 场景：回落到仓库相对路径
			if _, err := os.Stat("D:/opencode/SuperPM/uploads"); err == nil {
				cfg.LocalRoot = "D:/opencode/SuperPM/uploads"
			} else {
				// 尝试从当前工作目录推导
				if wd, _ := os.Getwd(); wd != "" {
					cand := filepath.Join(wd, "uploads")
					if strings.Contains(wd, "SuperPM") {
						// 在 backend 子目录时 wd=.../SuperPM/backend
						cand2 := filepath.Join(filepath.Dir(wd), "uploads")
						if _, err := os.Stat(filepath.Dir(wd)); err == nil {
							_ = cand
							cfg.LocalRoot = cand2
						} else {
							cfg.LocalRoot = cand
						}
					}
				}
			}
		}
	}
	cfg.S3Endpoint = strings.TrimSpace(os.Getenv("S3_ENDPOINT"))
	if cfg.S3Endpoint == "" {
		cfg.S3Endpoint = strings.TrimSpace(os.Getenv("FORJA_S3_ENDPOINT"))
	}
	cfg.S3Bucket = strings.TrimSpace(os.Getenv("S3_BUCKET"))
	if cfg.S3Bucket == "" {
		cfg.S3Bucket = strings.TrimSpace(os.Getenv("FORJA_S3_BUCKET"))
	}
	cfg.S3Region = strings.TrimSpace(os.Getenv("S3_REGION"))
	if cfg.S3Region == "" {
		cfg.S3Region = strings.TrimSpace(os.Getenv("FORJA_S3_REGION"))
		if cfg.S3Region == "" {
			cfg.S3Region = "us-east-1"
		}
	}
	cfg.S3AccessKey = strings.TrimSpace(os.Getenv("S3_ACCESS_KEY"))
	if cfg.S3AccessKey == "" {
		cfg.S3AccessKey = strings.TrimSpace(os.Getenv("FORJA_S3_ACCESS_KEY"))
		cfg.S3Secret = strings.TrimSpace(os.Getenv("FORJA_S3_SECRET"))
		if cfg.S3Secret == "" {
			cfg.S3Secret = strings.TrimSpace(os.Getenv("AWS_SECRET_ACCESS_KEY"))
		}
	} else {
		cfg.S3Secret = strings.TrimSpace(os.Getenv("S3_SECRET_KEY"))
		if cfg.S3Secret == "" {
			cfg.S3Secret = strings.TrimSpace(os.Getenv("S3_SECRET"))
		}
	}
	// 兼容 FORJA_ 前缀变体
	if v := strings.TrimSpace(os.Getenv("FORJA_S3_ACCESS_KEY")); v != "" {
		cfg.S3AccessKey = v
	}
	if v := strings.TrimSpace(os.Getenv("FORJA_S3_SECRET_KEY")); v != "" {
		cfg.S3Secret = v
	}
	if v := strings.TrimSpace(os.Getenv("FORJA_S3_SECRET")); v != "" {
		cfg.S3Secret = v
	}
	if v := strings.TrimSpace(os.Getenv("S3_SECRET")); v != "" && cfg.S3Secret == "" {
		cfg.S3Secret = v
	}
	ps := strings.ToLower(strings.TrimSpace(os.Getenv("S3_PATH_STYLE")))
	if ps == "" {
		ps = strings.ToLower(strings.TrimSpace(os.Getenv("FORJA_S3_PATH_STYLE")))
	}
	cfg.S3PathStyle = ps == "true" || ps == "1" || ps == "yes"
	// MinIO 默认 path-style
	if cfg.Driver == "s3" && ps == "" {
		cfg.S3PathStyle = true
	}
	return cfg
}

func isWindows() bool {
	return filepath.Separator == '\\' || os.PathSeparator == '\\'
}

// 全局单例（按需初始化，测试可替换）
var (
	globalStore FileStore
	globalErr   error
)

func Get() (FileStore, error) {
	if globalStore != nil || globalErr != nil {
		return globalStore, globalErr
	}
	s, err := NewFromEnv()
	if err != nil {
		globalErr = err
		return nil, err
	}
	globalStore = s
	return globalStore, nil
}

// SetForTest 注入测试替身（传 nil 则重置）。
func SetForTest(s FileStore) {
	globalStore = s
	globalErr = nil
}

func Reset() {
	globalStore = nil
	globalErr = nil
}

// NewFromEnv 按 FORJA_STORAGE_DRIVER 装配驱动（local 默认，s3 走 minio-go）。
func NewFromEnv() (FileStore, error) {
	cfg := ConfigFromEnv()
	switch cfg.Driver {
	case "local":
		return NewLocal(cfg.LocalRoot)
	case "s3":
		return NewS3(cfg)
	default:
		return nil, fmt.Errorf("unknown storage driver %q (want local|s3)", cfg.Driver)
	}
}

// AllowedExtensions 扩展名白名单（§2c 原则：安全）。
var AllowedExtensions = map[string]bool{
	".pdf": true, ".docx": true, ".doc": true, ".xlsx": true, ".xls": true,
	".pptx": true, ".ppt": true, ".txt": true, ".md": true, ".csv": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".zip": true, ".rar": true, ".7z": true,
	".json": true, ".yaml": true, ".yml": true,
}

// MaxFileSize 单文件上限 20MB（§2c 约束）。
const MaxFileSize = 20 * 1024 * 1024

// ValidateFile 校验扩展名与大小（handler 层前置）。
func ValidateFile(filename string, size int64) error {
	if size > MaxFileSize {
		return fmt.Errorf("file too large: %d > %d (20MB)", size, MaxFileSize)
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == "" {
		return fmt.Errorf("missing file extension")
	}
	if !AllowedExtensions[ext] {
		return fmt.Errorf("file extension %q not allowed", ext)
	}
	return nil
}

// BuildKey 生成存储键 `{scope}/{projectID}/{yyyymm}/{uuid7}-{origName}`。
// scope: inbox | project
func BuildKey(scope, projectID, origName string) string {
	safe := sanitizeFilename(origName)
	if safe == "" {
		safe = "file"
	}
	ym := time.Now().Format("200601")
	uid, err := uuid.NewV7()
	uidStr := ""
	if err == nil {
		uidStr = uid.String()
	} else {
		uidStr = uuid.NewString()
	}
	return fmt.Sprintf("%s/%s/%s/%s-%s", scope, projectID, ym, uidStr, safe)
}

func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	// 去掉路径穿越与控制字符
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "..", "_")
	name = strings.TrimSpace(name)
	// 限制长度
	if len(name) > 128 {
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		if len(base) > 100 {
			base = base[:100]
		}
		name = base + ext
	}
	return name
}
