package cmd

import (
	"embed"
	"net/url"
	"os"
	"strings"

	"github.com/gobuffalo/pop/v6"
	"github.com/gobuffalo/pop/v6/logging"
	"github.com/lib/pq"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/supatype/auth/internal/storage"
)

var EmbeddedMigrations embed.FS

var migrateCmd = cobra.Command{
	Use:  "migrate",
	Long: "Migrate database strucutures. This will create new tables and add missing columns and indexes.",
	Run:  migrate,
}

func migrate(cmd *cobra.Command, args []string) {
	globalConfig := loadGlobalConfig(cmd.Context())
	dbURL := globalConfig.DB.URL
	if nu, err := storage.EnsurePostgresSearchPathInURL(dbURL, globalConfig.DB.Namespace); err == nil {
		dbURL = nu
	} else {
		logrus.WithError(err).Warn("could not normalize database URL (using config URL as-is)")
	}
	u, err := url.Parse(dbURL)
	if err != nil {
		logrus.Fatalf("%+v", errors.Wrap(err, "parsing db connection url"))
	}

	if globalConfig.DB.Driver == "" && globalConfig.DB.URL != "" {
		globalConfig.DB.Driver = u.Scheme
	}

	log := logrus.StandardLogger()

	pop.Debug = false
	if globalConfig.Logging.Level != "" {
		level, err := logrus.ParseLevel(globalConfig.Logging.Level)
		if err != nil {
			log.Fatalf("Failed to parse log level: %+v", err)
		}
		log.SetLevel(level)
		if level == logrus.DebugLevel {
			// Set to true to display query info
			pop.Debug = true
		}
		if level != logrus.DebugLevel {
			var noopLogger = func(lvl logging.Level, s string, args ...interface{}) {
			}
			// Hide pop migration logging
			pop.SetLogger(noopLogger)
		}
	}

	q := u.Query()
	q.Set("application_name", "auth_migrations")
	u.RawQuery = q.Encode()
	deets := &pop.ConnectionDetails{
		Dialect: globalConfig.DB.Driver,
		URL:     u.String(),
	}
	deets.Options = map[string]string{
		"migration_table_name": "schema_migrations",
		"Namespace":            globalConfig.DB.Namespace,
	}

	db, err := pop.NewConnection(deets)
	if err != nil {
		log.Fatalf("%+v", errors.Wrap(err, "opening db connection"))
	}
	defer db.Close()

	if err := db.Open(); err != nil {
		log.Fatalf("%+v", errors.Wrap(err, "checking database connection"))
	}

	storage.TightenPoolForMigration(db)

	// Persist default search_path for this database so every pooled connection
	// (including Pop migrator) sees a valid path on PostgreSQL 15+.
	if err := storage.TryAlterDatabaseSearchPathDefault(db, globalConfig.DB.Namespace); err != nil {
		logrus.WithError(err).Warn("could not ALTER DATABASE SET search_path (continuing; ensure DB role or URL sets search_path)")
	}

	if ns := strings.TrimSpace(globalConfig.DB.Namespace); ns != "" {
		setPath := "SET search_path TO " + pq.QuoteIdentifier(ns)
		if err := db.RawQuery(setPath).Exec(); err != nil {
			log.Fatalf("%+v", errors.Wrap(err, "setting search_path for migrations"))
		}
	}

	log.Debugf("Reading migrations from executable")
	box, err := pop.NewMigrationBox(EmbeddedMigrations, db)
	if err != nil {
		log.Fatalf("%+v", errors.Wrap(err, "creating db migrator"))
	}

	mig := box.Migrator

	log.Debugf("before status")

	if log.Level == logrus.DebugLevel {
		err = mig.Status(os.Stdout)
		if err != nil {
			log.Fatalf("%+v", errors.Wrap(err, "migration status"))
		}
	}

	// turn off schema dump
	mig.SchemaPath = ""

	count, err := mig.UpTo(0)
	if err != nil {
		log.Fatalf("%v", errors.Wrap(err, "running db migrations"))
	} else {
		log.WithField("count", count).Infof("GoTrue migrations applied successfully")
	}

	log.Debugf("after status")

	if log.Level == logrus.DebugLevel {
		err = mig.Status(os.Stdout)
		if err != nil {
			log.Fatalf("%+v", errors.Wrap(err, "migration status"))
		}
	}
}
