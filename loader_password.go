package main

import (
	"fmt"
	"io"
)

func readLoaderPassword(reader io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 4097))
	if err != nil {
		return "", fmt.Errorf("read database password from standard input: %w", err)
	}
	if len(data) > 4096 {
		return "", fmt.Errorf("database password from standard input exceeds 4096 bytes")
	}
	password := string(data)
	if !validDatabasePassword(password) {
		return "", fmt.Errorf("database password from standard input is invalid")
	}
	return password, nil
}
