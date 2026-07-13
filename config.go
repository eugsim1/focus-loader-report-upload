package main

import (
	"fmt"
	"gopkg.in/ini.v1"
	"strings"
)

func loadConfig(path string) (appConfig, error) {
	cfg, err := ini.Load(path)
	if err != nil {
		return appConfig{}, fmt.Errorf("load config %s: %w", path, err)
	}
	return appConfig{ini: cfg}, nil
}

func (cfg appConfig) table(key string) (string, error) {
	table := strings.TrimSpace(cfg.ini.Section("tables").Key(key).String())
	if table == "" {
		return "", fmt.Errorf("missing [tables] %s in focus.conf", key)
	}

	return cfg.qualifyTable(table), nil
}

func (cfg appConfig) optionalTable(keys ...string) (string, bool) {
	for _, key := range keys {
		table := strings.TrimSpace(cfg.ini.Section("tables").Key(key).String())
		if table != "" {
			return cfg.qualifyTable(table), true
		}
	}
	return "", false
}

func (cfg appConfig) defaultTable(table string) string {
	return cfg.qualifyTable(table)
}

func (cfg appConfig) qualifyTable(table string) string {
	schema := strings.TrimSpace(cfg.ini.Section("database").Key("schema").String())
	if schema == "" || strings.Contains(table, ".") {
		return table
	}
	return schema + "." + table
}
