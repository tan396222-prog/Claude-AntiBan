package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"text/tabwriter"
	"time"

	"github.com/sylvinus/agent-vm/internal/agentcli"
	"github.com/sylvinus/agent-vm/internal/workbench"
)

const regionHelp = `cage region list [--json]
cage region apply --preset ID [--dry-run] [--wait 10m]
cage region apply --timezone ZONE --locale xx_YY.UTF-8 [--browser-language xx-YY] [--dry-run] [--wait 10m]
cage region apply --follow-exit [--wait 10m]

One-click timezone and language: sets the guest timezone, system locale,
CLI environment (TZ, LANG, LC_ALL) and browser Accept-Language together,
through the running Cage service. A running environment restarts once.
`

// runRegion is a thin wrapper over the region.presets and region.apply Agent
// operations, so the CLI, Agent interface and workbench share one validator.
func runRegion(configPath string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(stdout, regionHelp)
		return 0
	}
	flags := flag.NewFlagSet("cage region "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	asJSON := flags.Bool("json", false, "print JSON")
	preset := flags.String("preset", "", "preset ID from cage region list")
	timezone := flags.String("timezone", "", "IANA timezone, e.g. America/Los_Angeles")
	locale := flags.String("locale", "", "system locale, e.g. en_US.UTF-8")
	browser := flags.String("browser-language", "", "browser language, derived from --locale when omitted")
	follow := flags.Bool("follow-exit", false, "follow the proxy exit's timezone and language automatically")
	dryRun := flags.Bool("dry-run", false, "validate without applying")
	wait := flags.Duration("wait", 10*time.Minute, "wait for the apply job; 0 returns immediately")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 {
		fmt.Fprint(stderr, regionHelp)
		return 2
	}
	switch args[0] {
	case "list":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		data, err := workbench.AgentCallTarget(ctx, configPath, workbench.AgentTarget{Scope: "local"}, "region.presets", nil)
		if err != nil {
			return fail(err)
		}
		if *asJSON {
			_, _ = stdout.Write(append(bytes.TrimSpace(data), '\n'))
			return 0
		}
		var view struct {
			Presets     []workbench.RegionPreset `json:"presets"`
			Recommended string                   `json:"recommended"`
			Current     string                   `json:"current"`
			Settings    struct {
				Timezone        string `json:"timezone"`
				Locale          string `json:"locale"`
				BrowserLanguage string `json:"browser_language"`
				AutoTimezone    bool   `json:"auto_timezone"`
				AutoLanguage    bool   `json:"auto_language"`
			} `json:"settings"`
		}
		if err := json.Unmarshal(data, &view); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "Current: %s  %s  %s (auto timezone=%v, auto language=%v)\n\n", view.Settings.Timezone, view.Settings.Locale, view.Settings.BrowserLanguage, view.Settings.AutoTimezone, view.Settings.AutoLanguage)
		table := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(table, "ID\tREGION\tTIMEZONE\tLOCALE\tBROWSER\t")
		for _, p := range view.Presets {
			mark := ""
			if p.ID == view.Current {
				mark += " [applied]"
			}
			if p.ID == view.Recommended {
				mark += " [matches exit]"
			}
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n", p.ID, p.LabelEN, p.Timezone, p.Locale, p.BrowserLanguage, mark)
		}
		_ = table.Flush()
		return 0
	case "apply":
		body, _ := json.Marshal(map[string]any{
			"preset": *preset, "timezone": *timezone, "locale": *locale, "browser_language": *browser,
			"follow_exit": *follow, "dry_run": *dryRun,
		})
		call := []string{"call", "region.apply", "--config", configPath, "--input", "-"}
		if !*dryRun && *wait > 0 {
			call = append(call, "--wait", wait.String())
		}
		return agentcli.Main(call, bytes.NewReader(body), stdout)
	default:
		fmt.Fprint(stderr, regionHelp)
		return 2
	}
}
