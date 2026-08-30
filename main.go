package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/belt-sh/harness-test/harness"
	"github.com/belt-sh/harness-test/runner"
	"github.com/belt-sh/harness-test/server"
)

func main() {
	var (
		harnessName  = flag.String("harness", "", "harness to test (or 'all')")
		mode         = flag.String("mode", "both", "test mode: headless, interactive, both, acp, or sdk")
		hooks        = flag.String("hooks", "mock", "hook source: mock (test scripts) or belt (real belt hooks)")
		listFlag     = flag.Bool("list", false, "list available harnesses")
		detectFlag   = flag.Bool("detect", false, "detect installed harnesses on this system")
		installFlag  = flag.String("install", "", "install belt hooks for a harness (name or 'detected')")
		installScope = flag.String("scope", "user", "install scope: user or project")
		serverOnly   = flag.Bool("server", false, "run mock server only (no tests)")
		intercept    = flag.Bool("intercept", false, "intercept all LLM traffic via /etc/hosts + TLS (requires root/Docker)")
	)
	flag.Parse()

	if *detectFlag {
		results := harness.DetectAll()
		sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })
		fmt.Printf("%-12s %-40s %-30s %s\n", "HARNESS", "PATH", "PROBES", "VERSION")
		for _, r := range results {
			path := r.Binary
			if path == "" {
				path = "—"
			}
			probes := "·"
			if len(r.Probes) > 0 {
				var ps []string
				for _, p := range r.Probes {
					ps = append(ps, string(p))
				}
				probes = strings.Join(ps, ", ")
			}
			fmt.Printf("%-12s %-40s %-30s %s\n", r.Name, path, probes, r.Version)
		}
		return
	}

	if *installFlag != "" {
		scope := harness.ScopeUser
		if *installScope == "project" {
			scope = harness.ScopeProject
		}

		var targets []string
		if *installFlag == "detected" {
			for _, r := range harness.DetectAll() {
				if r.Installed() || r.Configured() {
					targets = append(targets, r.Name)
				}
			}
			if len(targets) == 0 {
				fmt.Fprintln(os.Stderr, "no harnesses detected")
				os.Exit(1)
			}
		} else {
			for _, name := range strings.Split(*installFlag, ",") {
				targets = append(targets, strings.TrimSpace(name))
			}
		}

		sort.Strings(targets)
		scopeName := "user"
		if scope == harness.ScopeProject {
			scopeName = "project"
		}
		fmt.Printf("Installing belt hooks (%s scope)\n\n", scopeName)
		for _, name := range targets {
			result := harness.Install(name, scope)
			if result.Error != nil {
				fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", name, result.Error)
			} else if result.Merged {
				fmt.Printf("  ✓ %s: merged hooks into %s\n", name, result.HooksPath)
			} else {
				fmt.Printf("  ✓ %s: created %s\n", name, result.HooksPath)
			}
		}
		return
	}

	if *listFlag {
		fmt.Println("Available harnesses:")
		fmt.Printf("  %-12s %-10s %-8s %-10s %-5s %-5s\n", "NAME", "BINARY", "API", "HOOKS", "ACP", "SDK")
		names := make([]string, 0, len(harness.All))
		for name := range harness.All {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			h := harness.All[name]
			acp := "—"
			if len(h.ACPCmd) > 0 {
				acp = "✓"
			}
			sdk := "—"
			if len(h.SDKCmd) > 0 {
				sdk = "✓"
			}
			fmt.Printf("  %-12s %-10s %-8s %-10s %-5s %-5s\n",
				name, h.Binary, h.APIFormat, h.HookFormat, acp, sdk)
		}
		return
	}

	srv := server.New()

	if *serverOnly {
		fmt.Println("Mock inference server")
		fmt.Println("Endpoints: /v1/chat/completions, /v1/responses, /v1/messages, /v1/models")
		fmt.Println("Utilities: GET /log, GET /log/count, DELETE /log, POST /response")
		// For server-only mode we need a different listener setup
		// For now, start with the random port
		baseURL, err := srv.Start()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to start server: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Listening at %s\n", baseURL)
		select {} // block forever
	}

	if *harnessName == "" {
		fmt.Fprintln(os.Stderr, "usage: harness-test --harness <name|all>")
		fmt.Fprintln(os.Stderr, "       harness-test --list")
		fmt.Fprintln(os.Stderr, "       harness-test --server")
		os.Exit(1)
	}

	// Check if any target harness needs intercept
	needsIntercept := *intercept
	if !needsIntercept {
		var checkNames []string
		if *harnessName == "all" {
			for name := range harness.All {
				checkNames = append(checkNames, name)
			}
		} else {
			for _, name := range strings.Split(*harnessName, ",") {
				checkNames = append(checkNames, strings.TrimSpace(name))
			}
		}
		for _, name := range checkNames {
			if h, ok := harness.All[name]; ok && h.NeedsIntercept {
				needsIntercept = true
				break
			}
		}
	}

	var baseURL string
	var err error
	if needsIntercept {
		baseURL, err = srv.StartIntercept()
		if err == nil {
			proxyAddr, proxyErr := srv.StartProxy()
			if proxyErr != nil {
				fmt.Fprintf(os.Stderr, "warning: proxy start failed: %v\n", proxyErr)
			} else {
				fmt.Printf("MITM proxy at %s\n", proxyAddr)
			}
		}
	} else {
		baseURL, err = srv.Start()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start mock server: %v\n", err)
		os.Exit(1)
	}
	defer srv.Close()
	if needsIntercept {
		fmt.Printf("Mock server at %s (intercept: HTTPS on :443)\n\n", baseURL)
	} else {
		fmt.Printf("Mock server at %s\n\n", baseURL)
	}

	var targets []string
	if *harnessName == "all" {
		for name := range harness.All {
			targets = append(targets, name)
		}
		sort.Strings(targets)
	} else {
		for _, name := range strings.Split(*harnessName, ",") {
			name = strings.TrimSpace(name)
			if _, ok := harness.All[name]; !ok {
				fmt.Fprintf(os.Stderr, "unknown harness: %s\n", name)
				os.Exit(1)
			}
			targets = append(targets, name)
		}
	}

	totalPassed, totalFailed, totalSkipped := 0, 0, 0
	var failed []string
	var results []runner.Result

	for _, name := range targets {
		h := harness.All[name]
		srv.ClearLog()
		r := runner.New(h, srv, baseURL)
		r.SetMode(*mode)
		if *hooks == "belt" {
			r.SetHookSource(runner.HooksBelt)
		}
		if *intercept {
			r.SetIntercept(true)
		}
		result := r.Run()
		results = append(results, result)
		totalPassed += result.Passed
		totalFailed += result.Failed
		totalSkipped += result.Skipped
		if result.Failed > 0 {
			failed = append(failed, name)
		}
	}

	var totalDuration time.Duration
	fmt.Println("=== Summary ===")
	fmt.Printf("%-12s %-30s %6s %6s %6s %8s\n", "HARNESS", "VERSION", "PASS", "FAIL", "SKIP", "TIME")
	for _, r := range results {
		totalDuration += r.Duration
		ver := r.Version
		if ver == "" {
			ver = "—"
		}
		if len(ver) > 30 {
			ver = ver[:30]
		}
		fmt.Printf("%-12s %-30s %6d %6d %6d %8s\n", r.Harness, ver, r.Passed, r.Failed, r.Skipped, r.Duration.Round(time.Second))
	}
	fmt.Printf("%-12s %-30s %6d %6d %6d %8s\n", "TOTAL", "", totalPassed, totalFailed, totalSkipped, totalDuration.Round(time.Second))
	if len(failed) > 0 {
		fmt.Printf("failures: %s\n", strings.Join(failed, ", "))
		os.Exit(1)
	}
}

