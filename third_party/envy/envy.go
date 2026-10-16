// Package envy stands in for github.com/gobuffalo/envy, which pop imports.
//
// The real package runs godotenv.Overload on ./.env in its init, before main,
// so a .env in the server's working directory replaced every key it shared
// with the process environment. A dev stack runs the server in the project
// directory, which holds the project's .env, and the value compose put in the
// container lost to the file: SUPATYPE_MAILER_AUTOCONFIRM=false in the
// container and true in .env answered true. The server's rule is the other way
// round (internal/config/dotenv.go): the environment wins, and a file only
// fills keys the environment leaves unset.
//
// This keeps the two functions pop calls and drops the init. go.mod points the
// module path here with a replace directive.
package envy

import (
	"errors"
	"io/fs"
	"os"

	"github.com/joho/godotenv"
)

// Get returns the value of key in the environment, or fallback when it is unset.
func Get(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

// Load reads the named files, or ./.env when none are named, into the
// environment without replacing a key that is already set. A missing ./.env is
// not an error; a named file that is missing is.
func Load(files ...string) error {
	err := godotenv.Load(files...)
	if len(files) == 0 && errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
