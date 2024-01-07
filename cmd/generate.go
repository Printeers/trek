package cmd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v4/stdlib"
	"github.com/stripe/pg-schema-diff/pkg/diff"
	"github.com/stripe/pg-schema-diff/pkg/tempdb"
	"log"
	"os"
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
				log.Printf("Failed to run: %v\n", err)
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
			migrationsDir,
			initial,
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
			migrationsDir,
			migrationNumber == 1,
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
	migrationsDir string,
	initial bool,
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

	targetSQL, err := os.ReadFile(filepath.Join(wd, fmt.Sprintf("%s.sql", config.ModelName)))
	if err != nil {
		return "", fmt.Errorf("failed to read target sql: %w", err)
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

	// The tempDbFactory is used in plan generation to extract the new schema and validate the plan
	tempDbFactory, err := tempdb.NewOnInstanceFactory(ctx, func(ctx context.Context, dbName string) (*sql.DB, error) {
		copiedConfig := migrateConn.Config().Copy()
		copiedConfig.Database = dbName
		return openDbWithPgxConfig(copiedConfig)
	})
	if err != nil {
		panic("Generating the TempDbFactory failed")
	}
	defer tempDbFactory.Close()

	db, err := openDbWithPgxConfig(migrateConn.Config())
	if err != nil {
		panic("Generating the TempDbFactory failed")
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		panic("Generating the TempDbFactory failed")
	}

	plan, err := diff.GeneratePlan(ctx, conn, tempDbFactory, []string{string(targetSQL)}, diff.WithDataPackNewTables())
	if err != nil {
		panic("Generating the plan failed")
	}

	var statements []string
	for _, statement := range plan.Statements {
		stmt := statement.ToSQL()
		if stmt == "DROP TABLE \"schema_migrations\";" {
			continue
		}
		statements = append(statements, stmt)
	}

	return strings.Join(statements, "\n"), nil
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

func openDbWithPgxConfig(config *pgx.ConnConfig) (*sql.DB, error) {
	connPool := stdlib.OpenDB(*config)
	if err := connPool.Ping(); err != nil {
		connPool.Close()
		return nil, err
	}
	return connPool, nil
}
