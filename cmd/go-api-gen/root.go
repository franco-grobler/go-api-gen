package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/franco-grobler/go-api-gen/internal/config"
	"github.com/franco-grobler/go-api-gen/internal/gen"
	"github.com/franco-grobler/go-api-gen/internal/workspace"
)

// version is overridden at release time with -ldflags.
var version = "dev"

// options are the flags shared by every command that reads a workspace.
type options struct {
	configPath string
	verbose    bool
}

func newRootCmd() *cobra.Command {
	opts := &options{}

	root := &cobra.Command{
		Use:   "go-api-gen",
		Short: "Generate Go code and OpenAPI documents from composable specs",
		Long: `go-api-gen is a spec-first OpenAPI generator for Go monorepos.

Shared schemas are authored once in a shared root, generated once into a single
Go package, and imported by every service that references them. Each service
still gets a self-contained, tree-shaken OpenAPI document of its own, and the
unify command stitches them all into one platform-wide document.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	root.SetVersionTemplate("go-api-gen {{.Version}}\n")

	pf := root.PersistentFlags()
	pf.StringVarP(&opts.configPath, "config", "c", "",
		"config file (default: nearest go-api-gen.yaml, searching upward)")
	pf.BoolVarP(&opts.verbose, "verbose", "v", false,
		"list every file considered, not just the ones that changed")

	root.AddCommand(
		newBuildCmd(opts),
		newCheckCmd(opts),
		newUnifyCmd(opts),
	)
	return root
}

func newBuildCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "build",
		Short: "Generate every configured output",
		Long: `Generate every configured output.

Files whose content is unchanged are left alone, so build is safe to run on
every save and only touches what actually differs.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, g, err := load(opts)
			if err != nil {
				return err
			}
			changed, err := g.Write()
			if err != nil {
				return err
			}
			listed := changed
			if opts.verbose {
				listed = g.Paths()
			}
			for _, p := range listed {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", rel(cfg.Dir, p))
			}
			if len(changed) == 0 {
				_, _ = fmt.Fprintf(
					cmd.OutOrStdout(),
					"go-api-gen: %d files already up to date\n",
					len(g.Files()),
				)
				return nil
			}
			_, _ = fmt.Fprintf(
				cmd.OutOrStdout(),
				"go-api-gen: wrote %d of %d files\n",
				len(changed),
				len(g.Files()),
			)
			return nil
		},
	}
}

func newCheckCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Report whether committed output is up to date, writing nothing",
		Long: `Report whether committed output is up to date, writing nothing.

Generated output is deterministic, so a non-zero exit means a spec changed
without its generated code being regenerated. This is the CI drift gate.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, g, err := load(opts)
			if err != nil {
				return err
			}
			stale, err := g.Check()
			if err != nil {
				return err
			}
			if len(stale) == 0 {
				if opts.verbose {
					for _, p := range g.Paths() {
						_, _ = fmt.Fprintf(
							cmd.OutOrStdout(),
							"  %s\n",
							rel(cfg.Dir, p),
						)
					}
				}
				_, _ = fmt.Fprintf(
					cmd.OutOrStdout(),
					"go-api-gen: %d files up to date\n",
					len(g.Files()),
				)
				return nil
			}
			out := cmd.ErrOrStderr()
			_, _ = fmt.Fprintf(
				out,
				"go-api-gen: %d generated file(s) are out of date:\n",
				len(stale),
			)
			for _, p := range stale {
				_, _ = fmt.Fprintf(out, "  %s\n", rel(cfg.Dir, p))
			}
			_, _ = fmt.Fprintln(
				out,
				"Run \"go-api-gen build\" and commit the result.",
			)
			return silentError{fmt.Errorf("out of date")}
		},
	}
}

func newUnifyCmd(opts *options) *cobra.Command {
	var output string
	var stdout bool

	cmd := &cobra.Command{
		Use:   "unify",
		Short: "Stitch every service spec into one unified OpenAPI document",
		Long: `Stitch every service spec into one unified OpenAPI document.

The per-service documents that build produces each describe one service. The
unified document describes the platform as a whole, for an API gateway, a docs
portal, or a client that spans services.

Paths merge method by method, so two services may serve different methods of
the same path but not the same one. Operations that declare no tags of their
own are tagged with their service's title, so the merged document stays
navigable. Every schema any service reaches is inlined as a component, so the
result depends on no other file.

The output path comes from unified.to in the config unless --output is
given.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, g, err := load(opts)
			if err != nil {
				return err
			}
			data, err := g.Unified()
			if err != nil {
				return err
			}
			if stdout {
				_, err := cmd.OutOrStdout().Write(data)
				return err
			}
			path, err := g.UnifiedPath(output)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return err
			}
			fmt.Fprintf(
				cmd.OutOrStdout(),
				"go-api-gen: wrote %s\n",
				rel(cfg.Dir, path),
			)
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVarP(
		&output,
		"output",
		"o",
		"",
		"write the unified spec here (overrides unified.to)",
	)
	f.BoolVar(
		&stdout,
		"stdout",
		false,
		"write the unified spec to standard output instead of a file",
	)
	cmd.MarkFlagsMutuallyExclusive("output", "stdout")
	return cmd
}

// load resolves the config and runs the generator, which every command needs
// before it can report anything.
func load(opts *options) (*config.Config, *gen.Generator, error) {
	cfg, err := loadConfig(opts.configPath)
	if err != nil {
		return nil, nil, err
	}
	ws, err := workspace.Load(cfg)
	if err != nil {
		return nil, nil, err
	}
	g := gen.New(ws)
	if err := g.Run(); err != nil {
		return nil, nil, err
	}
	return cfg, g, nil
}

func loadConfig(path string) (*config.Config, error) {
	if path != "" {
		return config.Load(path)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return config.Discover(cwd)
}

func rel(base, path string) string {
	if r, err := filepath.Rel(base, path); err == nil {
		return r
	}
	return path
}
