package cmd

import (
	"embed"
	"os"

	"github.com/gobuffalo/pop/v6"
	"github.com/gobuffalo/pop/v6/logging"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/supatype/server/internal/auth/storage"
)

var EmbeddedMigrations embed.FS

var migrateCmd = cobra.Command{
	Use:  "migrate",
	Long: "Migrate database strucutures. This will create new tables and add missing columns and indexes.",
	Run:  migrate,
}

func migrate(cmd *cobra.Command, args []string) {
	globalConfig := loadGlobalConfig(cmd.Context())
	deets, err := storage.MigrationConnectionDetails(
		globalConfig.DB.URL, globalConfig.DB.Driver, globalConfig.DB.Namespace, "auth_migrations")
	if err != nil {
		logrus.Fatalf("%+v", errors.Wrap(err, "parsing db connection url"))
	}

	log := logrus.StandardLogger()
	configureMigrateLogging(log, globalConfig.Logging.Level != "")

	db := openMigrationDB(cmd, log, deets)
	defer db.Close()
	if err := storage.SettleMigrationSearchPath(db, globalConfig.DB.Namespace); err != nil {
		log.Fatalf("%+v", err)
	}

	runMigrations(log, db)
}

// configureMigrateLogging shows Pop's query and migration logging only at debug. The level
// itself was set once by loadGlobalConfig; with none configured, Pop keeps the logger that
// set up.
func configureMigrateLogging(log *logrus.Logger, levelConfigured bool) {
	pop.Debug = false
	if !levelConfigured {
		return
	}
	if log.Level == logrus.DebugLevel {
		// Set to true to display query info
		pop.Debug = true
	} else {
		// Hide pop migration logging
		pop.SetLogger(func(lvl logging.Level, s string, args ...interface{}) {})
	}
}

// openMigrationDB opens the migrate connection and waits for the database to answer.
func openMigrationDB(cmd *cobra.Command, log *logrus.Logger, deets *pop.ConnectionDetails) *pop.Connection {
	db, err := pop.NewConnection(deets)
	if err != nil {
		log.Fatalf("%+v", errors.Wrap(err, "opening db connection"))
	}

	if err := db.Open(); err != nil {
		log.Fatalf("%+v", errors.Wrap(err, "checking database connection"))
	}

	// Open builds a pool and contacts nothing, so without this the first real
	// contact is the first migration query, and it exits the process. A
	// database that is still starting is a timing problem, not a configuration
	// one: serving waits it out through DialWithRetry, and migrating did not,
	// which lost the container on any host slow enough to open the gap.
	if err := storage.WaitReachable(cmd.Context(), db); err != nil {
		log.Fatalf("%+v", errors.Wrap(err, "waiting for the database"))
	}
	return db
}

// runMigrations applies the embedded auth migrations, printing status around them at debug.
func runMigrations(log *logrus.Logger, db *pop.Connection) {
	log.Debugf("Reading migrations from executable")
	box, err := pop.NewMigrationBox(EmbeddedMigrations, db)
	if err != nil {
		log.Fatalf("%+v", errors.Wrap(err, "creating db migrator"))
	}

	mig := box.Migrator

	log.Debugf("before status")
	printMigrationStatus(log, mig)

	// turn off schema dump
	mig.SchemaPath = ""

	count, err := mig.UpTo(0)
	if err != nil {
		log.Fatalf("%v", errors.Wrap(err, "running db migrations"))
	} else {
		log.WithField("count", count).Infof("auth migrations applied successfully")
	}

	log.Debugf("after status")
	printMigrationStatus(log, mig)
}

// printMigrationStatus writes the migration status table to stdout at debug level.
func printMigrationStatus(log *logrus.Logger, mig pop.Migrator) {
	if log.Level != logrus.DebugLevel {
		return
	}
	if err := mig.Status(os.Stdout); err != nil {
		log.Fatalf("%+v", errors.Wrap(err, "migration status"))
	}
}
