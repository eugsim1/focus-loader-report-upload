package main

import (
	"errors"
	"fmt"
	"github.com/oracle/oci-go-sdk/v65/common"
	"os"
	"strings"
	"time"
)

func currentDateTime() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

func formatElapsed(d time.Duration) string {
	totalSeconds := int64(d.Round(time.Second).Seconds())
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
}

func value(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return common.String(s)
}

func objectFileDate(objectName string) string {
	parts := strings.Split(objectName, "/")
	if len(parts) <= 3 {
		return ""
	}
	return strings.Join(parts[1:4], "-")
}

func safeWorkFileName(objectName string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		`"`, "_",
		"<", "_",
		">", "_",
		"|", "_",
		" ", "_",
		"\t", "_",
		"\r", "_",
		"\n", "_",
	)
	return replacer.Replace(objectName)
}

func warnRemove(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Printf("WARN: could not remove %s: %v\n", path, err)
	}
}

func valueInt64(ptr *int64) int64 {
	if ptr == nil {
		return 0
	}
	return *ptr
}

func substr(s string, start, max int) string {
	if start >= len(s) {
		return ""
	}
	end := start + max
	if end > len(s) {
		end = len(s)
	}
	return s[start:end]
}
