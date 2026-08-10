package main

import "testing"

func TestParseArgsTNSGUI(t *testing.T) {
	cmd, err := parseArgs([]string{"-tns-gui", "-tns-gui-listen", "127.0.0.1:9090"})
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if !cmd.tnsGUI {
		t.Fatal("tnsGUI = false, want true")
	}
	if cmd.tnsGUIListen != "127.0.0.1:9090" {
		t.Fatalf("tnsGUIListen = %q, want 127.0.0.1:9090", cmd.tnsGUIListen)
	}
}

func TestParseArgsTNSGUIDefaultListener(t *testing.T) {
	cmd, err := parseArgs([]string{"-tns-gui"})
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if cmd.tnsGUIListen != "127.0.0.1:8080" {
		t.Fatalf("tnsGUIListen = %q, want 127.0.0.1:8080", cmd.tnsGUIListen)
	}
}
