// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/othershore-studio/llamingo/internal/taita"
	"golang.org/x/term"
)

// runAdmin is the `taita admin ...` console toolkit: inspect and repair the
// account registry directly on the server box (reassign the admin, reset a
// password) when the web flow has locked someone out. It opens accounts.db in the
// data dir and needs no running server — in fact the server must be stopped, since
// the bbolt DB is single-writer.
func runAdmin(args []string) {
	fs := flag.NewFlagSet("taita admin", flag.ExitOnError)
	dataFlag := fs.String("data", "", "data dir holding accounts.db (default: $TAITA_DATA, then ~/.taita/data)")
	fs.Usage = adminUsage
	_ = fs.Parse(args)
	rest := fs.Args()
	if len(rest) == 0 {
		adminUsage()
		os.Exit(2)
	}

	dataDir := firstNonEmpty(*dataFlag, os.Getenv("TAITA_DATA"), defaultDir("data"))
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		fatalf("create data dir %s: %v", dataDir, err)
	}
	dbPath := filepath.Join(dataDir, "accounts.db")
	acc, err := taita.OpenAccounts(dbPath)
	if err != nil {
		fatalf("open %s: %v\n(is the taita server still running? stop it first — the accounts DB is single-writer)", dbPath, err)
	}
	defer acc.Close()

	switch rest[0] {
	case "list":
		adminList(acc)
	case "set":
		adminSet(acc, rest[1:])
	case "passwd":
		adminPasswd(acc, rest[1:])
	default:
		adminUsage()
		os.Exit(2)
	}
}

func adminUsage() {
	fmt.Fprint(os.Stderr, `taita admin — inspect or recover accounts (run on the server box, server stopped)

usage:
  taita admin [-data DIR] list                  list accounts and roles
  taita admin [-data DIR] set NAME [admin|user]  set a role (default admin; creates the account if missing)
  taita admin [-data DIR] passwd NAME            set/reset a password (prompted, no echo)
`)
}

func adminList(acc taita.Accounts) {
	list, err := acc.List()
	if err != nil {
		fatalf("list: %v", err)
	}
	if len(list) == 0 {
		fmt.Println("(no accounts yet)")
		return
	}
	for _, a := range list {
		pw := "no password"
		if a.HasPassword() {
			pw = "password set"
		}
		fmt.Printf("%-20s  %-5s  %s\n", a.Name, a.Role, pw)
	}
}

func adminSet(acc taita.Accounts, args []string) {
	if len(args) < 1 {
		fatalf("usage: taita admin set NAME [admin|user]")
	}
	name := args[0]
	role := taita.RoleAdmin
	if len(args) > 1 {
		switch args[1] {
		case "admin":
			role = taita.RoleAdmin
		case "user":
			role = taita.RoleUser
		default:
			fatalf("role must be admin or user, got %q", args[1])
		}
	}

	a, err := acc.Get(name)
	switch {
	case errors.Is(err, taita.ErrNotFound):
		a = &taita.Account{Name: name, Created: time.Now().UTC()}
	case err != nil:
		fatalf("lookup %q: %v", name, err)
	}
	a.Role = role
	if err := acc.Put(a); err != nil {
		fatalf("save: %v", err)
	}
	fmt.Printf("%s is now %s\n", a.Name, role)
	if role == taita.RoleAdmin && !a.HasPassword() {
		fmt.Printf("warning: %s has no password — run `taita admin passwd %s`\n", a.Name, a.Name)
	}
}

func adminPasswd(acc taita.Accounts, args []string) {
	if len(args) < 1 {
		fatalf("usage: taita admin passwd NAME")
	}
	name := args[0]
	a, err := acc.Get(name)
	if errors.Is(err, taita.ErrNotFound) {
		fatalf("no such account %q (create it with `taita admin set %s`)", name, name)
	} else if err != nil {
		fatalf("lookup %q: %v", name, err)
	}

	fmt.Print("New password: ")
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		fatalf("read password: %v", err)
	}
	if len(pw) == 0 {
		fatalf("password is empty")
	}
	if err := a.SetPassword(string(pw)); err != nil {
		fatalf("hash password: %v", err)
	}
	if err := acc.Put(a); err != nil {
		fatalf("save: %v", err)
	}
	fmt.Printf("password updated for %s\n", a.Name)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "taita admin: "+format+"\n", args...)
	os.Exit(1)
}
