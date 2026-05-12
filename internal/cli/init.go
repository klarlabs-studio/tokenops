package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/felixgeelhaar/tokenops/internal/config"
)

// initFlags holds wizard inputs. Defaults are resolved per-OS via XDG
// rules; flag overrides win so CI / containerised installs can pin paths
// without touching env vars.
type initFlags struct {
	configPath  string
	storagePath string
	rulesRoot   string
	repoID      string
	force       bool
	printOnly   bool
}

func newInitCmd() *cobra.Command {
	f := &initFlags{}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold the TokenOps config file and enable storage + rules",
		Long: `init writes an opinionated config to $XDG_CONFIG_HOME/tokenops/config.yaml
(or ~/.config/tokenops/config.yaml) that enables:

  - sqlite event storage at $XDG_DATA_HOME/tokenops/events.db
  - the rules subsystem rooted at the current working directory
  - audit on the security domain bus

Provider URLs are left empty; configure them via TOKENOPS_PROVIDER_*_URL or
edit the resulting config. Re-running init is a no-op unless --force is
passed; --print-only emits the YAML without touching disk.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInit(cmd, f)
		},
	}
	cmd.Flags().StringVar(&f.configPath, "config-path", "", "override config file path (defaults to XDG location)")
	cmd.Flags().StringVar(&f.storagePath, "storage-path", "", "override events.db path (defaults to XDG data dir)")
	cmd.Flags().StringVar(&f.rulesRoot, "rules-root", "", "override rule scan root (defaults to current working directory)")
	cmd.Flags().StringVar(&f.repoID, "repo-id", "", "rule corpus identifier prepended to source IDs (defaults to basename of rules root)")
	cmd.Flags().BoolVar(&f.force, "force", false, "overwrite an existing config file")
	cmd.Flags().BoolVar(&f.printOnly, "print-only", false, "render the resulting YAML to stdout without writing")
	return cmd
}

func runInit(cmd *cobra.Command, f *initFlags) error {
	configPath, err := resolveInitConfigPath(f.configPath)
	if err != nil {
		return err
	}
	storagePath, err := resolveInitStoragePath(f.storagePath)
	if err != nil {
		return err
	}
	rulesRoot, repoID, err := resolveInitRulesRoot(f.rulesRoot, f.repoID)
	if err != nil {
		return err
	}

	cfg := config.Default()
	cfg.Storage.Enabled = true
	cfg.Storage.Path = storagePath
	cfg.Rules.Enabled = true
	cfg.Rules.Root = rulesRoot
	cfg.Rules.RepoID = repoID

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if f.printOnly {
		_, err := cmd.OutOrStdout().Write(data)
		return err
	}

	if existing, err := os.Stat(configPath); err == nil && !existing.IsDir() {
		if !f.force {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"tokenops init: config already exists at %s — re-run with --force to overwrite\n",
				configPath,
			)
			return nil
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", configPath, err)
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(configPath), err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", configPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(storagePath), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(storagePath), err)
	}

	fmt.Fprintf(cmd.OutOrStdout(),
		"wrote %s\nstorage: %s\nrules root: %s (repo_id=%s)\nnext: configure providers and run `tokenops demo` or `tokenops start`\n",
		configPath, storagePath, rulesRoot, repoID,
	)
	return nil
}

func resolveInitConfigPath(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "tokenops", "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "tokenops", "config.yaml"), nil
}

func resolveInitStoragePath(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "tokenops", "events.db"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tokenops", "events.db"), nil
}

func resolveInitRulesRoot(rootOverride, repoOverride string) (string, string, error) {
	root := rootOverride
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", "", err
		}
		root = wd
	}
	repo := repoOverride
	if repo == "" {
		repo = filepath.Base(root)
	}
	return root, repo, nil
}
