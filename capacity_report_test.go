package main

import (
	"strings"
	"testing"
)

func TestShouldUseUserDictionaryUsesSessionUser(t *testing.T) {
	tests := []struct {
		name        string
		owner       string
		sessionUser string
		want        bool
	}{
		{name: "same user", owner: "FOCUS_TEST_GO_V1", sessionUser: "FOCUS_TEST_GO_V1", want: true},
		{name: "admin reading target schema", owner: "FOCUS_TEST_GO_V1", sessionUser: "ADMIN", want: false},
		{name: "quoted and mixed case", owner: `"focus_test_go_v1"`, sessionUser: "FOCUS_TEST_GO_V1", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldUseUserDictionary(tt.owner, tt.sessionUser); got != tt.want {
				t.Fatalf("shouldUseUserDictionary(%q, %q) = %v, want %v", tt.owner, tt.sessionUser, got, tt.want)
			}
		})
	}
}

func TestCapacityQueriesUseExplicitSYSViews(t *testing.T) {
	tests := []struct {
		name  string
		query string
		view  string
	}{
		{name: "user segments", query: userSegmentBytesQuery, view: "SYS.USER_SEGMENTS"},
		{name: "all segments", query: allSegmentBytesQuery, view: "SYS.ALL_SEGMENTS"},
		{name: "user tables", query: userTableStatsQuery, view: "SYS.USER_TABLES"},
		{name: "all tables", query: allTableStatsQuery, view: "SYS.ALL_TABLES"},
		{name: "user partitions", query: userPartitionStatsQuery, view: "SYS.USER_TAB_PARTITIONS"},
		{name: "all partitions", query: allPartitionStatsQuery, view: "SYS.ALL_TAB_PARTITIONS"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upperQuery := strings.ToUpper(tt.query)
			if !strings.Contains(upperQuery, "FROM "+tt.view) {
				t.Fatalf("query does not select from explicit dictionary view %s: %s", tt.view, tt.query)
			}
			if strings.Contains(upperQuery, "FOCUS_TEST_GO_V1.") {
				t.Fatalf("query incorrectly embeds the application schema: %s", tt.query)
			}
		})
	}
}

func TestEstimateBytesPerRow(t *testing.T) {
	if got := estimateBytesPerRow(1_000, 4); got != 250 {
		t.Fatalf("estimateBytesPerRow(1000, 4) = %v, want 250", got)
	}
	if got := estimateBytesPerRow(1_000, 0); got != 0 {
		t.Fatalf("estimateBytesPerRow with no statistics = %v, want 0", got)
	}
}
