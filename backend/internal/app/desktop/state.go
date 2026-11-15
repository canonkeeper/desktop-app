// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"cmp"
	"context"
	"errors"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	accountdomain "canonkeeper/backend/internal/domain/accountlink"
	"canonkeeper/backend/internal/domain/aisettings"
	service "canonkeeper/backend/internal/services/desktop"
)

// usageLimit is how long getDesktopState waits for the count of the data directory (specs/2026-11-13-desk-settings.md
// ST2.2): a saga of many books and the cluster are many files, and the page asks again while it counts.
const usageLimit = 2 * time.Second

// usageFresh is how long a count that ended answers a request without a new count: the page asks again while the server
// counts, and a count that ended between two of its requests is the place it waits for.
const usageFresh = 30 * time.Second

// appState is getDesktopState of this run: the version the build names, the data directory and its copies, the welcome
// settings.json keeps (specs/2026-11-13-desk-upgrade.md UG5.1) and where the app goes in the network (ST4.3, DT11.4).
func appState(data DataDir, version string, welcome service.WelcomeSettings, network func() []service.NetworkUse, proxy func() string) service.ReadAppState {
	return service.ReadAppState{
		Version: version,
		DataDir: data.Root,
		Usage:   &usageMeter{root: data.Root, limit: usageLimit, count: placeOf, now: time.Now},
		Backups: backupList{directory: data.Backups()},
		Network: network,
		Proxy:   proxy,
		Welcome: welcome,
	}
}

// reaches is the link with an account as the list of the network reads it.
type reaches interface{ Reaches() bool }

// networkUses is where the app goes in the network in this build and its settings (ST4.3, DT11.4): the open app goes
// nowhere; a linked app, or one whose link started, goes to the server CanonKeeper of its build
// (specs/2026-11-13-desk-account.md AC2.7); a build that updates itself goes to the hosts of its updates — others
// (specs/2026-11-13-desk-updates.md UB1.6). One line a host: what the app goes to one host for stands in its one line.
// A slice that makes the app go somewhere adds its host and what for here.
func networkUses(account reaches, site string, others ...func() []service.NetworkUse) func() []service.NetworkUse {
	return func() []service.NetworkUse {
		var uses []service.NetworkUse
		if account.Reaches() {
			uses = append(uses, service.NetworkUse{Host: accountdomain.HostOf(site), Purpose: "связь с аккаунтом: имя, тариф и остатки"})
		}
		for _, other := range others {
			for _, use := range other() {
				at := slices.IndexFunc(uses, func(known service.NetworkUse) bool { return known.Host == use.Host })
				if at < 0 {
					uses = append(uses, use)
					continue
				}
				uses[at].Purpose += "; " + use.Purpose
			}
		}
		return uses
	}
}

// ownNetwork is the provider of «Свой ИИ» while its address is kept (specs/2026-11-14-desk-journal.md JR4.1): the questions,
// the reading of the books, the list of the models and the check of the connection go there; an address of this computer
// is a program of this computer, with no promise that the text goes no further.
func ownNetwork(ai func() aisettings.Settings) func() []service.NetworkUse {
	return func() []service.NetworkUse {
		own := ai().Own
		if own == nil || own.BaseURL == "" || own.Host() == "" {
			return nil
		}
		purpose := "помощник через «Свой ИИ»"
		if own.Local() {
			purpose = "программа на этом компьютере, помощник через «Свой ИИ»"
		}
		return []service.NetworkUse{{Host: own.Host(), Purpose: purpose}}
	}
}

// proxyOfTheNetwork is the host of the proxy of the environment the requests of the backend go through (JR4.3), without a
// name or a password — "" when no address of the list goes through one: the server of the link while the app reaches it,
// the provider of «Свой ИИ» by its kept address. The gateway and the client of the site take the proxy by
// http.ProxyFromEnvironment, and so does this: the proxy of the scheme of the address, never for this computer, not for a
// host NO_PROXY names.
func proxyOfTheNetwork(lookup func(string) string, account reaches, site string, ai func() aisettings.Settings) func() string {
	return func() string {
		var targets []string
		if account.Reaches() {
			targets = append(targets, site)
		}
		if own := ai().Own; own != nil && own.BaseURL != "" {
			targets = append(targets, own.BaseURL)
		}
		for _, target := range targets {
			if host := proxyFor(lookup, target); host != "" {
				return host
			}
		}
		return ""
	}
}

// proxyFor is the host of the proxy a request to the address goes through, "" when it goes straight.
func proxyFor(lookup func(string) string, address string) string {
	target, err := url.Parse(address)
	if err != nil || target.Hostname() == "" {
		return ""
	}
	var value string
	switch target.Scheme {
	case "https":
		value = environmentOf(lookup, "HTTPS_PROXY", "https_proxy")
	case "http":
		value = environmentOf(lookup, "HTTP_PROXY", "http_proxy")
	}
	if value == "" || !throughProxy(target, environmentOf(lookup, "NO_PROXY", "no_proxy")) {
		return ""
	}
	proxy, err := url.Parse(value)
	if err != nil || proxy.Host == "" {
		if proxy, err = url.Parse("http://" + value); err != nil || proxy.Host == "" {
			return ""
		}
	}
	return proxy.Host
}

func environmentOf(lookup func(string) string, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(lookup(name)); value != "" {
			return value
		}
	}
	return ""
}

// throughProxy is the rule of http.ProxyFromEnvironment for an address: this computer goes straight, and so does a host
// NO_PROXY names — «*», an address or a network, a domain with its subdomains, «.domain» for the subdomains, with a port or
// without.
func throughProxy(target *url.URL, noProxy string) bool {
	host, port := strings.ToLower(target.Hostname()), target.Port()
	if port == "" {
		port = map[string]string{"https": "443", "http": "80"}[target.Scheme]
	}
	ip := net.ParseIP(host)
	if host == "localhost" || ip != nil && ip.IsLoopback() {
		return false
	}
	for _, entry := range strings.Split(strings.ToLower(noProxy), ",") {
		entry = strings.TrimSpace(entry)
		switch {
		case entry == "":
			continue
		case entry == "*":
			return false
		}
		if _, network, err := net.ParseCIDR(entry); err == nil {
			if ip != nil && network.Contains(ip) {
				return false
			}
			continue
		}
		name, entryPort := entry, ""
		if at := strings.LastIndex(entry, ":"); at > 0 && !strings.Contains(entry[at+1:], "]") && net.ParseIP(entry) == nil {
			name, entryPort = entry[:at], entry[at+1:]
		}
		name = strings.Trim(name, "[]")
		if entryPort != "" && entryPort != port {
			continue
		}
		if entryIP := net.ParseIP(name); entryIP != nil {
			if ip != nil && entryIP.Equal(ip) {
				return false
			}
			continue
		}
		name = strings.TrimPrefix(name, "*")
		whole := !strings.HasPrefix(name, ".")
		if whole {
			name = "." + name
		}
		if ip == nil && (strings.HasSuffix(host, name) || whole && host == name[1:]) {
			return false
		}
	}
	return true
}

// usageMeter counts the place of the data directory in the background: a request starts a count unless one goes on or
// one ended less than usageFresh ago, waits for it no longer than limit and answers the last known place meanwhile (ST2.2).
type usageMeter struct {
	root  string
	limit time.Duration
	count func(root string) int64
	now   func() time.Time

	mu      sync.Mutex
	bytes   int64
	known   bool
	counted time.Time
	running chan struct{}
}

func (meter *usageMeter) Measure(ctx context.Context) service.DiskUsage {
	meter.mu.Lock()
	if meter.running == nil && meter.known && meter.now().Sub(meter.counted) < usageFresh {
		defer meter.mu.Unlock()
		return service.DiskUsage{Bytes: meter.bytes, Known: true}
	}
	done := meter.running
	if done == nil {
		done = make(chan struct{})
		meter.running = done
		go meter.run(done)
	}
	meter.mu.Unlock()
	timer := time.NewTimer(meter.limit)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	case <-ctx.Done():
	}
	meter.mu.Lock()
	defer meter.mu.Unlock()
	return service.DiskUsage{Bytes: meter.bytes, Known: meter.known, Counting: meter.running == done}
}

func (meter *usageMeter) run(done chan struct{}) {
	bytes := meter.count(meter.root)
	meter.mu.Lock()
	meter.bytes, meter.known, meter.counted, meter.running = bytes, true, meter.now(), nil
	meter.mu.Unlock()
	close(done)
}

// placeOf is the sum of the sizes of the regular files under root. Links are neither followed nor counted — the home
// of Codex links to the profile of the bundle — and what cannot be read is left out.
func placeOf(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() {
			if info, err := entry.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// backupVersionLength bounds what the version of a copy may be: a version of the app, not a text.
const backupVersionLength = 64

// backupList lists the complete copies of backups/ (ST2.1, ST2.3): the newest first by the time in their names, as
// many as the start keeps.
type backupList struct{ directory string }

func (list backupList) List() ([]service.Backup, error) {
	entries, err := os.ReadDir(list.directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var copies []service.Backup
	for _, entry := range entries {
		path := filepath.Join(list.directory, entry.Name())
		_, made, ok := backupOf(entry.Name())
		if !ok || !entry.IsDir() || !backupFinished(path) {
			continue
		}
		at, err := time.Parse(backupTime, made)
		if err != nil {
			continue
		}
		copies = append(copies, service.Backup{Made: at.UTC(), Version: versionOf(path)})
	}
	slices.SortFunc(copies, func(a, b service.Backup) int { return cmp.Compare(b.Made.UnixNano(), a.Made.UnixNano()) })
	return copies[:min(len(copies), backupsKept)], nil
}

// versionOf reads the version a copy holds; a copy without one, or with something that is no version, names none.
func versionOf(copy string) string {
	raw, err := os.ReadFile(filepath.Join(copy, backupVersion))
	if err != nil || len(raw) > backupVersionLength+1 {
		return ""
	}
	version := strings.TrimSpace(string(raw))
	if version == "" || strings.ContainsFunc(version, func(r rune) bool { return r < '!' || r > '~' }) {
		return ""
	}
	return version
}
