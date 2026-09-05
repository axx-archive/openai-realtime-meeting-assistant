package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalDatabaseBoundary(t *testing.T) {
	for _, dsn := range []string{"postgres://runtime@127.0.0.1/stride_business_proof_qa?sslmode=disable", "host=/tmp/stride-proof port=5432 user=runtime dbname=stride_business_proof_qa", "postgres://runtime@[::1]/stride_business_proof_qa"} {
		if _, e := localConfig(dsn); e != nil {
			t.Fatalf("expected local configuration: %v", e)
		}
	}
	for _, dsn := range []string{"", "postgres://runtime@example.com/stride_business_proof_qa", "postgres://runtime@127.0.0.1/bonfire", "postgres://runtime@127.0.0.1,example.com/stride_business_proof_qa", "postgres://runtime@127.0.0.1/stride_business_proof_qa?host=remote.example"} {
		if _, e := localConfig(dsn); e == nil {
			t.Fatal("unsafe database configuration accepted")
		}
	}
}

func TestPrivateFilesCannotClobberOrFollowSymlinks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "proof")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := privateDir(dir); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(dir, "state.json")
	if e := exclusive(p, []byte("first")); e != nil {
		t.Fatal(e)
	}
	if e := exclusive(p, []byte("replacement")); e == nil {
		t.Fatal("clobbered existing state")
	}
	got, _ := os.ReadFile(p)
	if string(got) != "first" {
		t.Fatal("state changed")
	}
	link := filepath.Join(dir, "link")
	if e := os.Symlink(p, link); e != nil {
		t.Fatal(e)
	}
	if e := exclusive(link, []byte("replacement")); e == nil {
		t.Fatal("followed symlink")
	}
	if e := os.Chmod(dir, 0755); e != nil {
		t.Fatal(e)
	}
	if e := privateDir(dir); e == nil {
		t.Fatal("public state directory accepted")
	}
}

func TestExplicitLiveAuthorizationRequiredBeforeSideEffects(t *testing.T) {
	old := os.Args
	defer func() { os.Args = old }()
	path := filepath.Join(t.TempDir(), "must-not-exist")
	os.Args = []string{"proof", "prepare", "--state-dir", path}
	if e := run(); e == nil {
		t.Fatal("missing live authorization accepted")
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("side effect before authorization")
	}
}
