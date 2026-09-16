// Command scenarios runs the narrated end-to-end stories.
//
// By default it starts the test router in process, so `go run ./cmd/scenarios`
// needs nothing running and no Docker. Point -url at a router that is already up —
// the containerised one, or a real agrirouter, once one implements this — and
// it runs the same code against that instead.
//
// The scenarios assert as they narrate: a claim that does not hold ends its
// scenario and the command exits non-zero.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/testrouter"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/scenarios"
)

func main() {
	url := flag.String("url", "",
		"run against this router instead of starting one in process")
	only := flag.Int("scenario", 0, "run just this one, by number")
	list := flag.Bool("list", false, "print the scenarios and exit")
	batch := flag.Bool("batch", false,
		"answer the scenarios' questions with their defaults instead of asking")
	flag.Parse()

	if *list {
		for _, s := range scenarios.All() {
			fmt.Printf("%d  %s\n", s.Number, s.Title)
		}
		return
	}

	baseURL := *url
	if baseURL == "" {
		server := httptest.NewServer(testrouter.New().Handler())
		defer server.Close()
		baseURL = server.URL
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	print := func(line string) { fmt.Println(line) }

	// One thing in this protocol cannot be answered by a program: which of a
	// platform's records a canonical object is. Where a scenario reaches that, it
	// asks, and this is who answers. Piped or redirected input has nobody behind
	// it, so the defaults stand and the run stays automatable.
	var ask scenarios.Asker
	if !*batch && attended() {
		ask = askOnTerminal(bufio.NewReader(os.Stdin))
	}

	var err error
	if *only > 0 {
		scenario, ok := scenarios.ByNumber(*only)
		if !ok {
			fmt.Fprintf(os.Stderr, "no scenario %d\n", *only)
			os.Exit(2)
		}
		err = scenario.ExecuteWith(ctx, baseURL, print, ask)
	} else {
		err = scenarios.RunAllWith(ctx, baseURL, print, ask)
	}

	fmt.Println()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

// attended reports whether a person is at the other end of stdin. A pipe or a
// file is not somebody to ask.
func attended() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// askOnTerminal puts a scenario's question to the terminal and reads the answer.
//
// The question itself is already on screen — the narration prints it as a step,
// so it reads as part of the story rather than as an interruption — and this
// adds the choices and the prompt.
func askOnTerminal(in *bufio.Reader) scenarios.Asker {
	return func(question string, choices []string) (string, error) {
		for i, choice := range choices {
			suffix := ""
			if i == 0 {
				suffix = "  (default)"
			}
			fmt.Printf("      [%d] %s%s\n", i+1, choice, suffix)
		}

		for {
			fmt.Printf("      choose 1-%d, or press enter: ", len(choices))
			line, err := in.ReadString('\n')
			if errors.Is(err, io.EOF) {
				// The terminal went away mid-question. Taking the default is the
				// only thing left that keeps the run meaningful.
				fmt.Println()
				return choices[0], nil
			}
			if err != nil {
				return "", err
			}

			answer := strings.TrimSpace(line)
			switch {
			case answer == "":
				return choices[0], nil
			case isChoice(answer, choices):
				return canonical(answer, choices), nil
			}
			if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(choices) {
				return choices[n-1], nil
			}
			fmt.Printf("      %q is not one of them.\n", answer)
		}
	}
}

func isChoice(answer string, choices []string) bool {
	return canonical(answer, choices) != ""
}

// canonical returns the choice the answer names, ignoring case, or "".
func canonical(answer string, choices []string) string {
	for _, choice := range choices {
		if strings.EqualFold(answer, choice) {
			return choice
		}
	}
	return ""
}
