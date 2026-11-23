// Copyright IBM Corp. 2026
// SPDX-License-Identifier: MPL-2.0

// Package cmd implements the hedgetrimmer command line.
package cmd

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/YakDriver/hedgetrimmer/internal/audit"
	"github.com/YakDriver/hedgetrimmer/internal/config"
	"github.com/spf13/cobra"
)

var (
	cfgFile string
	repo    string
	output  string
	force   bool
)

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

var rootCmd = &cobra.Command{
	Use:   "hedgetrimmer",
	Short: "Audit a GitHub repository's branches for cleanup",
	Long: `hedgetrimmer sorts a GitHub repository's branches into buckets, from
"quick to check and likely deletable" to "keep", and prints a markdown
worksheet you can paste into an issue. It is read-only: it never deletes or
changes anything.

Run it inside a clone with the remote fetched. It needs git and an
authenticated gh CLI.`,
	Version:      version(),
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE:         runAudit,
}

var auditCmd = &cobra.Command{
	Use:          "audit",
	Short:        "Audit branches and print a markdown worksheet (default)",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE:         runAudit,
}

var initCmd = &cobra.Command{
	Use:          "init",
	Short:        "Write a commented starter " + config.DefaultFile,
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE:         runInit,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: "+config.DefaultFile+" if present)")
	for _, c := range []*cobra.Command{rootCmd, auditCmd} {
		c.Flags().StringVar(&repo, "repo", "", `GitHub repository "owner/name" (overrides config)`)
		c.Flags().StringVarP(&output, "output", "o", "", "write the report to a file instead of stdout")
	}
	initCmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config file")
	rootCmd.AddCommand(auditCmd, initCmd)
}

func runAudit(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return err
	}
	if repo != "" {
		if o, n, ok := strings.Cut(repo, "/"); !ok || o == "" || n == "" {
			return fmt.Errorf(`--repo must be "owner/name", got %q`, repo)
		}
		cfg.Repo = repo
	}
	roster, err := cfg.Roster()
	if err != nil {
		return err
	}
	if len(roster) == 0 {
		fmt.Fprintln(os.Stderr, "warning: no maintainers configured; non-bot branches without an open PR will all land in R3")
	}

	res, err := audit.Run(cmd.Context(), cfg, roster, time.Now())
	if err != nil {
		return err
	}

	w := cmd.OutOrStdout()
	if output != "" {
		f, err := os.Create(output)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	return audit.WriteMarkdown(w, cfg, res)
}

func runInit(cmd *cobra.Command, _ []string) error {
	path := cfgFile
	if path == "" {
		path = config.DefaultFile
	}
	flag := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flag = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flag, 0o644)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s already exists; use --force to overwrite", path)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(config.Template); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
	return nil
}

func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	v := info.Main.Version
	if v == "" {
		v = "dev"
	}
	if i := slices.IndexFunc(info.Settings, func(s debug.BuildSetting) bool {
		return s.Key == "vcs.revision" && len(s.Value) >= 7
	}); i >= 0 {
		return v + " (" + info.Settings[i].Value[:7] + ")"
	}
	return v
}
