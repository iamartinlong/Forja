package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"forja/backend/internal/model"
	"forja/backend/internal/storage"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "copy" {
		fmt.Fprintf(os.Stderr, "Usage: %s copy --from <driver> --to <driver> [--dry-run]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Drivers: local | s3 (env FORJA_STORAGE_DRIVER, LOCAL_ROOT, S3_* as per storage.Config)\n")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("copy", flag.ExitOnError)
	from := fs.String("from", "", "source driver (local|s3)")
	to := fs.String("to", "", "target driver (s3|local)")
	dryRun := fs.Bool("dry-run", false, "list keys without copying")
	// allow --from local  style without =
	_ = fs.Parse(os.Args[2:])
	if *from == "" || *to == "" {
		fmt.Fprintln(os.Stderr, "--from and --to required")
		fs.Usage()
		os.Exit(2)
	}
	*from = strings.ToLower(strings.TrimSpace(*from))
	*to = strings.ToLower(strings.TrimSpace(*to))
	if *from == *to {
		log.Fatalf("from and to must differ")
	}
	// 构造源/目标 FileStore：通过临时覆写 env FORJA_STORAGE_DRIVER
	srcStore, err := storeForDriver(*from)
	if err != nil {
		log.Fatalf("source store (%s): %v", *from, err)
	}
	dstStore, err := storeForDriver(*to)
	if err != nil {
		log.Fatalf("target store (%s): %v", *to, err)
	}
	// DB 连接（复用 model.InitDB）
	ctx := context.Background()
	if err := model.InitDB(ctx); err != nil {
		log.Fatalf("db init: %v", err)
	}
	defer model.CloseDB()
	keys, err := collectKeys(ctx)
	if err != nil {
		log.Fatalf("collect keys: %v", err)
	}
	if len(keys) == 0 {
		fmt.Println("no keys to copy")
		return
	}
	fmt.Printf("found %d keys: %s -> %s (dry-run=%v)\n", len(keys), *from, *to, *dryRun)
	if *dryRun {
		for _, k := range keys {
			fmt.Println(k)
		}
		return
	}
	copied, errs := storage.Copy(ctx, srcStore, dstStore, keys)
	fmt.Printf("copied %d/%d\n", copied, len(keys))
	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "errors (%d):\n", len(errs))
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, " ", e)
		}
		os.Exit(1)
	}
}

func storeForDriver(driver string) (storage.FileStore, error) {
	// 临时环境覆写，仅影响本次构造
	orig := os.Getenv("FORJA_STORAGE_DRIVER")
	os.Setenv("FORJA_STORAGE_DRIVER", driver)
	s, err := storage.NewFromEnv()
	// 恢复
	if orig == "" {
		os.Unsetenv("FORJA_STORAGE_DRIVER")
	} else {
		os.Setenv("FORJA_STORAGE_DRIVER", orig)
	}
	// 重置全局单例，避免交叉污染
	storage.Reset()
	return s, err
}

func collectKeys(ctx context.Context) ([]string, error) {
	if model.Pool == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := model.Pool.Query(ctx, `SELECT storage_key FROM inbox_attachments UNION ALL SELECT storage_key FROM project_files`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		if k != "" {
			keys = append(keys, k)
		}
	}
	return keys, rows.Err()
}
