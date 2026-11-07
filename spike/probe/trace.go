// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bufio"
	"flag"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

type httpClient struct{}

// status sends GET with an explicit Host header and optional token and Origin.
func (httpClient) status(url, host, token, origin string) int {
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return -1
	}
	request.Host = host
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return -1
	}
	response.Body.Close()
	return response.StatusCode
}

var (
	connectLine = regexp.MustCompile(`^(\d+)\s+.*\bconnect\((\d+), \{sa_family=(AF_INET6?|AF_UNIX|AF_NETLINK)(.*?)\}, \d+\)\s*=\s*(.*)$`)
	inetAddr    = regexp.MustCompile(`sin_port=htons\((\d+)\), sin_addr=inet_addr\("([^"]+)"\)`)
	inet6Addr   = regexp.MustCompile(`sin6_port=htons\((\d+)\).*inet_pton\(AF_INET6, "([^"]+)"`)
	unixPath    = regexp.MustCompile(`sun_path=@?"([^"]*)"`)
	execLine    = regexp.MustCompile(`^(\d+)\s+.*\bexecve\("([^"]+)"`)
)

// runTrace reads `strace -f -e trace=connect,execve` output and lists every connect() by the
// program that made it: loopback, Unix sockets and everything else (outbound).
func runTrace(args []string) error {
	flags := flag.NewFlagSet("trace", flag.ExitOnError)
	in := flags.String("in", "", "strace output")
	out := flags.String("out", "-", "result JSON")
	flags.Parse(args)
	file, err := os.Open(*in)
	if err != nil {
		return err
	}
	defer file.Close()
	programs := map[string]string{}
	outbound := map[string]int{}
	loopback := map[string]int{}
	unix := map[string]int{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 16<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if match := execLine.FindStringSubmatch(line); match != nil {
			programs[match[1]] = match[2]
			continue
		}
		match := connectLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		pid, family, rest, outcome := match[1], match[3], match[4], match[5]
		program := programs[pid]
		if program == "" {
			program = "pid " + pid
		}
		program = program[strings.LastIndexAny(program, "/\\")+1:]
		switch family {
		case "AF_UNIX":
			if path := unixPath.FindStringSubmatch(rest); path != nil {
				unix[program+" -> "+path[1]]++
			}
		case "AF_INET", "AF_INET6":
			address, port := "", ""
			if parts := inetAddr.FindStringSubmatch(rest); parts != nil {
				port, address = parts[1], parts[2]
			} else if parts := inet6Addr.FindStringSubmatch(rest); parts != nil {
				port, address = parts[1], parts[2]
			}
			key := program + " -> " + address + ":" + port + " = " + firstWord(outcome)
			if address == "127.0.0.1" || address == "::1" {
				loopback[key]++
			} else {
				outbound[key]++
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return writeJSON(*out, map[string]any{
		"outbound": outbound, "loopback": summarizeLoopback(loopback), "unix": unix,
		"programs": uniquePrograms(programs),
	})
}

func firstWord(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return text
	}
	if len(fields) > 1 && strings.HasPrefix(fields[1], "E") {
		return fields[0] + " " + fields[1]
	}
	return fields[0]
}

// summarizeLoopback folds ephemeral ports of the probe's own servers into one line per program
// and outcome, keeping DNS (port 53) and other well-known ports visible.
func summarizeLoopback(loopback map[string]int) map[string]int {
	folded := map[string]int{}
	for key, count := range loopback {
		parts := strings.SplitN(key, " -> ", 2)
		address := parts[1]
		if !strings.Contains(address, ":53 ") {
			address = "loopback (probe servers)" + address[strings.Index(address, " = "):]
		}
		folded[parts[0]+" -> "+address] += count
	}
	return folded
}

func uniquePrograms(programs map[string]string) []string {
	set := map[string]bool{}
	for _, program := range programs {
		set[program[strings.LastIndexAny(program, "/\\")+1:]] = true
	}
	list := make([]string, 0, len(set))
	for program := range set {
		list = append(list, program)
	}
	sort.Strings(list)
	return list
}
