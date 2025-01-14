package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v4"
	"github.com/spf13/cobra"

	"github.com/printeers/trek/internal"
)

//nolint:gocognit,cyclop
func NewGenerateCommand() *cobra.Command {
	var (
		dev       bool
		cleanup   bool
		overwrite bool
		stdout    bool
		check     bool
	)

	generateCmd := &cobra.Command{
		Use:   "generate [migration-name]",
		Short: "Generate the migrations for a pgModeler file",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			internal.InitializeFlags(cmd)
		},
		Args: func(cmd *cobra.Command, args []string) error {
			if stdout {
				if len(args) != 0 {
					//nolint:goerr113
					return errors.New("pass no name for stdout generation")
				}
			} else {
				if len(args) == 0 {
					//nolint:goerr113
					return errors.New("pass the name of the migration")
				} else if len(args) > 1 {
					//nolint:goerr113
					return errors.New("expecting one migration name, use lower-kebab-case for the migration name")
				}

				if !internal.RegexpMigrationName.MatchString(args[0]) {
					//nolint:goerr113
					return errors.New("migration name must be lower-kebab-case and must not start or end with a number or dash")
				}
			}

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			wd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get working directory: %w", err)
			}

			config, err := internal.ReadConfig(wd)
			if err != nil {
				return fmt.Errorf("failed to read config: %w", err)
			}

			migrationsDir, err := internal.GetMigrationsDir(wd)
			if err != nil {
				return fmt.Errorf("failed to get migrations directory: %w", err)
			}

			migrationFiles, err := internal.FindMigrations(migrationsDir, true)
			if err != nil {
				return fmt.Errorf("failed to find migrations: %w", err)
			}

			var initialFunc, continuousFunc func() error

			if stdout {
				initialFunc = func() error {
					var tmpDir string
					tmpDir, err = os.MkdirTemp("", "trek-")
					if err != nil {
						return fmt.Errorf("failed to create temporary directory: %w", err)
					}

					if check {
						err = checkAll(ctx, config, wd, tmpDir, migrationsDir)
						if err != nil {
							return err
						}
					}

					err = runWithStdout(ctx, config, wd, tmpDir, migrationsDir, len(migrationFiles) == 0)
					if err != nil {
						return err
					}

					//nolint:wrapcheck
					return os.RemoveAll(tmpDir)
				}

				continuousFunc = func() error {
					var tmpDir string
					tmpDir, err = os.MkdirTemp("", "trek-")
					if err != nil {
						return fmt.Errorf("failed to create temporary directory: %w", err)
					}

					err = runWithStdout(ctx, config, wd, tmpDir, migrationsDir, len(migrationFiles) == 0)
					if err != nil {
						return err
					}

					//nolint:wrapcheck
					return os.RemoveAll(tmpDir)
				}
			} else {
				migrationName := args[0]
				var newMigrationFilePath string
				var migrationNumber uint
				newMigrationFilePath, migrationNumber, err = internal.GetNewMigrationFilePath(
					migrationsDir,
					uint(len(migrationFiles)),
					migrationName,
					overwrite,
				)
				if err != nil {
					return fmt.Errorf("failed to get new migration file path: %w", err)
				}

				defer func() {
					if dev && cleanup {
						if _, err = os.Stat(newMigrationFilePath); err == nil {
							err = os.Remove(newMigrationFilePath)
							if err != nil {
								log.Printf("Failed to delete new migration file: %v\n", err)
							}
						}
					}
				}()

				initialFunc = func() error {
					var tmpDir string
					tmpDir, err = os.MkdirTemp("", "trek-")
					if err != nil {
						return fmt.Errorf("failed to create temporary directory: %w", err)
					}

					var updated bool
					updated, err = runWithFile(ctx, config, wd, tmpDir, migrationsDir, newMigrationFilePath, migrationNumber)
					if err != nil {
						return err
					}

					if updated && check {
						err = checkAll(ctx, config, wd, tmpDir, migrationsDir)
						if err != nil {
							return err
						}

						log.Println("Done checking")
					}

					//nolint:wrapcheck
					return os.RemoveAll(tmpDir)
				}
				continuousFunc = initialFunc
			}

			err = initialFunc()
			if err != nil {
				return err
			}

			if dev {
				for {
					time.Sleep(time.Millisecond * 100)
					err = continuousFunc()
					if err != nil {
						log.Printf("Failed to run: %v\n", err)
					}
				}
			}

			return nil
		},
	}

	generateCmd.Flags().BoolVar(&dev, "dev", false, "Watch for file changes and automatically regenerate the migration file") //nolint:lll
	generateCmd.Flags().BoolVar(&cleanup, "cleanup", true, "Remove the generated migrations file. Only works with --dev")
	generateCmd.Flags().BoolVar(&overwrite, "overwrite", false, "Overwrite existing files")
	generateCmd.Flags().BoolVar(&stdout, "stdout", false, "Output migration statements to stdout")
	generateCmd.Flags().BoolVar(&check, "check", true, "Run checks after generating the migration")

	return generateCmd
}

func setupDatabase(
	ctx context.Context,
	tmpDir,
	name string,
	port uint32,
) (
	*embeddedpostgres.EmbeddedPostgres,
	*pgx.Conn,
	string,
	error,
) {
	postgres, dsn := internal.NewPostgresDatabase(filepath.Join(tmpDir, name), port)
	err := postgres.Start()
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to start %q database: %w", name, err)
	}

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to connect to %q database: %w", name, err)
	}

	return postgres, conn, dsn, nil
}

//nolint:gocognit,cyclop
func runWithStdout(
	ctx context.Context,
	config *internal.Config,
	wd,
	tmpDir,
	migrationsDir string,
	initial bool,
) error {
	updated, err := checkIfUpdated(config, wd)
	if err != nil {
		return fmt.Errorf("failed to check if model has been updated: %w", err)
	}
	if updated {
		targetPostgres, targetConn, _, err := setupDatabase(ctx, tmpDir, "target", 5432)
		defer func() {
			if targetConn != nil {
				_ = targetConn.Close(ctx)
			}
			if targetPostgres != nil {
				_ = targetPostgres.Stop()
			}
		}()
		if err != nil {
			return fmt.Errorf("failed to setup target database: %w", err)
		}

		migratePostgres, migrateConn, _, err := setupDatabase(ctx, tmpDir, "migrate", 5433)
		defer func() {
			if migrateConn != nil {
				_ = migrateConn.Close(ctx)
			}
			if migratePostgres != nil {
				_ = migratePostgres.Stop()
			}
		}()
		if err != nil {
			return fmt.Errorf("failed to setup migrate database: %w", err)
		}

		statements, err := generateMigrationStatements(
			ctx,
			config,
			wd,
			tmpDir,
			migrationsDir,
			initial,
			targetConn,
			migrateConn,
		)
		if err != nil {
			return fmt.Errorf("failed to generate migration statements: %w", err)
		}

		file, err := os.CreateTemp("", "migration")
		if err != nil {
			return fmt.Errorf("failed get temporary migration file: %w", err)
		}

		err = os.WriteFile(
			file.Name(),
			[]byte(statements),
			0o600,
		)
		if err != nil {
			return fmt.Errorf("failed to write temporary migration file: %w", err)
		}

		err = internal.RunHook(wd, "generate-migration-post", &internal.HookOptions{
			Args: []string{file.Name()},
		})
		if err != nil {
			return fmt.Errorf("failed to run hook: %w", err)
		}

		tmpStatementBytes, err := os.ReadFile(file.Name())
		if err != nil {
			return fmt.Errorf("failed to read temporary migration file: %w", err)
		}
		statements = string(tmpStatementBytes)

		err = os.Remove(file.Name())
		if err != nil {
			return fmt.Errorf("failed to delete temporary migration file: %w", err)
		}

		fmt.Println("")
		fmt.Println("--")
		fmt.Println(statements)
		fmt.Println("--")
	}

	return nil
}

//nolint:gocognit,cyclop
func runWithFile(
	ctx context.Context,
	config *internal.Config,
	wd,
	tmpDir,
	migrationsDir,
	newMigrationFilePath string,
	migrationNumber uint,
) (bool, error) {
	updated, err := checkIfUpdated(config, wd)
	if err != nil {
		return false, fmt.Errorf("failed to check if model has been updated: %w", err)
	}
	if updated {
		if _, err = os.Stat(newMigrationFilePath); err == nil {
			err = os.Remove(newMigrationFilePath)
			if err != nil {
				return false, fmt.Errorf("failed to delete generated migration file: %w", err)
			}
		}

		targetPostgres, targetConn, _, err := setupDatabase(ctx, tmpDir, "target", 5432)
		defer func() {
			if targetConn != nil {
				_ = targetConn.Close(ctx)
			}
			if targetPostgres != nil {
				_ = targetPostgres.Stop()
			}
		}()
		if err != nil {
			return false, fmt.Errorf("failed to setup target database: %w", err)
		}

		migratePostgres, migrateConn, _, err := setupDatabase(ctx, tmpDir, "migrate", 5433)
		defer func() {
			if migrateConn != nil {
				_ = migrateConn.Close(ctx)
			}
			if migratePostgres != nil {
				_ = migratePostgres.Stop()
			}
		}()
		if err != nil {
			return false, fmt.Errorf("failed to setup migrate database: %w", err)
		}

		statements, err := generateMigrationStatements(
			ctx,
			config,
			wd,
			tmpDir,
			migrationsDir,
			migrationNumber == 1,
			targetConn,
			migrateConn,
		)
		if err != nil {
			return false, fmt.Errorf("failed to generate migration statements: %w", err)
		}

		//nolint:gosec
		err = os.WriteFile(
			newMigrationFilePath,
			[]byte(statements),
			0o644,
		)
		if err != nil {
			return false, fmt.Errorf("failed to write migration file: %w", err)
		}
		log.Println("Wrote migration file")

		err = internal.RunHook(wd, "generate-migration-post", &internal.HookOptions{
			Args: []string{newMigrationFilePath},
		})
		if err != nil {
			return false, fmt.Errorf("failed to run hook: %w", err)
		}

		err = writeTemplateFiles(config, migrationNumber)
		if err != nil {
			return false, fmt.Errorf("failed to write template files: %w", err)
		}

		return true, nil
	}

	return false, nil
}

func checkIfUpdated(config *internal.Config, wd string) (bool, error) {
	m, err := os.ReadFile(filepath.Join(wd, fmt.Sprintf("%s.dbm", config.ModelName)))
	if err != nil {
		return false, fmt.Errorf("failed to read model file: %w", err)
	}
	mStr := strings.TrimSuffix(string(m), "\n")
	if mStr == "" || mStr == modelContent {
		return false, nil
	}
	modelContent = mStr

	log.Println("Changes detected")

	return true, nil
}

func writeTemplateFiles(config *internal.Config, newVersion uint) error {
	for _, ts := range config.Templates {
		dir := filepath.Dir(ts.Path)
		err := os.MkdirAll(dir, 0o755)
		if err != nil {
			return fmt.Errorf("failed to create %q: %w", dir, err)
		}

		data, err := internal.ExecuteConfigTemplate(ts, newVersion)
		if err != nil {
			return fmt.Errorf("failed to execute template: %w", err)
		}

		err = os.WriteFile(ts.Path, []byte(*data), 0o600)
		if err != nil {
			return fmt.Errorf("failed to write file: %w", err)
		}
	}

	return nil
}

var (
	//nolint:gochecknoglobals
	modelContent = ""
)

//nolint:cyclop
func generateMigrationStatements(
	ctx context.Context,
	config *internal.Config,
	wd,
	tmpDir,
	migrationsDir string,
	initial bool,
	targetConn,
	migrateConn *pgx.Conn,
) (string, error) {
	log.Println("Generating migration statements")

	err := internal.PgModelerExportToFile(
		filepath.Join(wd, fmt.Sprintf("%s.dbm", config.ModelName)),
		filepath.Join(wd, fmt.Sprintf("%s.sql", config.ModelName)),
	)
	if err != nil {
		return "", fmt.Errorf("failed to export model: %w", err)
	}

	go func() {
		err = internal.PgModelerExportToPng(
			filepath.Join(wd, fmt.Sprintf("%s.dbm", config.ModelName)),
			filepath.Join(wd, fmt.Sprintf("%s.png", config.ModelName)),
		)
		if err != nil {
			log.Printf("Failed to export png: %v\n", err)
		}
	}()

	err = internal.CreateUsers(ctx, migrateConn, config.DatabaseUsers)
	if err != nil {
		return "", fmt.Errorf("failed to create migrate users: %w", err)
	}

	err = internal.CreateUsers(ctx, targetConn, config.DatabaseUsers)
	if err != nil {
		return "", fmt.Errorf("failed to create target users: %w", err)
	}

	err = executeTargetSQL(ctx, config, wd, targetConn)
	if err != nil {
		return "", fmt.Errorf("failed to execute target sql: %w", err)
	}

	if initial {
		// If we are developing the schema initially, there will be no diffs,
		// and we want to copy over the schema file to the initial migration file
		var input []byte
		input, err = os.ReadFile(filepath.Join(wd, fmt.Sprintf("%s.sql", config.ModelName)))
		if err != nil {
			return "", fmt.Errorf("failed to read sql file: %w", err)
		}

		return string(input), nil
	}

	err = executeMigrateSQL(migrationsDir, migrateConn)
	if err != nil {
		return "", fmt.Errorf("failed to execute migrate sql: %w", err)
	}

	statements, err := internal.Migra(internal.DSN(migrateConn, "disable"), internal.DSN(targetConn, "disable"))
	if err != nil {
		return "", fmt.Errorf("failed to run migra: %w", err)
	}

	// Filter stuff from go-migrate that doesn't exist in the target db, and we don't have and need anyway
	filter := []string{
		"alter table \"public\".\"schema_migrations\" drop constraint \"schema_migrations_pkey\";\n\n",
		"drop index if exists \"public\".\"schema_migrations_pkey\";\n\n",
		"drop table \"public\".\"schema_migrations\";\n\n",
	}
	for _, f := range filter {
		statements = strings.ReplaceAll(statements, f, "")
	}
	statements = strings.Trim(statements, "\n")

	extraStatements, err := generateMissingPermissionStatements(ctx, tmpDir, statements, targetConn, migrateConn)
	if err != nil {
		return "", fmt.Errorf("failed to generate missing permission statements: %w", err)
	}

	var output string
	if statements != "" {
		output += statements
	}
	if statements != "" && extraStatements != "" {
		output += "\n\n"
	}
	if extraStatements != "" {
		output += "-- Statements generated automatically, please review:\n" + extraStatements
	}
	if output != "" {
		output += "\n"
	}

	return output, nil
}

func executeMigrateSQL(migrationsDir string, migrateConn *pgx.Conn) error {
	m, err := migrate.New(fmt.Sprintf("file://%s", migrationsDir), internal.DSN(migrateConn, "disable"))
	if err != nil {
		return fmt.Errorf("failed to create migrate: %w", err)
	}
	err = m.Up()
	if err != nil {
		return fmt.Errorf("failed to up migrations: %w", err)
	}

	return nil
}

func executeTargetSQL(ctx context.Context, config *internal.Config, wd string, targetConn *pgx.Conn) error {
	targetSQL, err := os.ReadFile(filepath.Join(wd, fmt.Sprintf("%s.sql", config.ModelName)))
	if err != nil {
		return fmt.Errorf("failed to read target sql: %w", err)
	}

	_, err = targetConn.Exec(ctx, string(targetSQL))
	if err != nil {
		return fmt.Errorf("failed to execute target sql: %w", err)
	}

	return nil
}

func generateMissingPermissionStatements(
	ctx context.Context,
	tmpDir,
	statements string,
	targetConn,
	migrateConn *pgx.Conn,
) (string, error) {
	_, err := migrateConn.Exec(ctx, statements)
	if err != nil {
		return "", fmt.Errorf("failed to apply generated migration: %w", err)
	}

	pgDumpOptions := []string{
		"--schema-only",
		"--exclude-table=public.schema_migrations",
	}

	targetDump, err := internal.PgDump(internal.DSN(targetConn, "disable"), pgDumpOptions)
	if err != nil {
		//nolint:wrapcheck
		return "", err
	}

	migrateDump, err := internal.PgDump(internal.DSN(migrateConn, "disable"), pgDumpOptions)
	if err != nil {
		//nolint:wrapcheck
		return "", err
	}

	targetDumpFile := filepath.Join(tmpDir, "target.sql")
	err = os.WriteFile(targetDumpFile, []byte(targetDump), 0o600)
	if err != nil {
		return "", fmt.Errorf("failed to write target.sql file: %w", err)
	}

	migrateDumpFile := filepath.Join(tmpDir, "migrate.sql")
	err = os.WriteFile(migrateDumpFile, []byte(migrateDump), 0o600)
	if err != nil {
		return "", fmt.Errorf("failed to write migrate.sql file: %w", err)
	}

	diffCmd := exec.Command(
		"diff",
		"--minimal",
		"--unchanged-line-format=",
		"--old-line-format=",
		"--new-line-format=%L",
		migrateDumpFile,
		targetDumpFile,
	)
	diffCmd.Stderr = os.Stderr

	output, err := diffCmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if !(errors.As(err, &ee) && ee.ExitCode() != 0) {
			return "", fmt.Errorf("failed to run diff: %w %s", err, string(output))
		}
	}

	var lines []string
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "ALTER ") {
			lines = append(lines, line)
		}
	}

	return strings.Join(lines, "\n"), nil
}
