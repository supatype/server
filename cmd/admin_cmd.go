package cmd

import (
	"fmt"

	"github.com/gofrs/uuid"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/supatype/server/internal/auth/models"
	"github.com/supatype/server/internal/auth/storage"
	"github.com/supatype/server/internal/conf"
)

var autoconfirm, isAdmin bool
var audience string

func getAudience(c *conf.GlobalConfiguration) string {
	if audience == "" {
		return c.JWT.Aud
	}

	return audience
}

func adminCmd() *cobra.Command {
	var adminCmd = &cobra.Command{
		Use: "admin",
	}

	adminCmd.AddCommand(&adminCreateUserCmd, &adminDeleteUserCmd)
	adminCmd.PersistentFlags().StringVarP(&audience, "aud", "a", "", "Set the new user's audience")

	adminCreateUserCmd.Flags().BoolVar(&autoconfirm, "confirm", false, "Automatically confirm user without sending an email")
	adminCreateUserCmd.Flags().BoolVar(&isAdmin, "admin", false, "Create user with admin privileges")

	return adminCmd
}

var adminCreateUserCmd = cobra.Command{
	Use: "createuser",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) < 2 {
			logrus.Fatal("Not enough arguments to createuser command. Expected at least email and password values")
			return
		}

		execWithConfigAndArgs(cmd, adminCreateUser, args)
	},
}

var adminDeleteUserCmd = cobra.Command{
	Use: "deleteuser",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) < 1 {
			logrus.Fatal("Not enough arguments to deleteuser command. Expected at least ID or email")
			return
		}

		execWithConfigAndArgs(cmd, adminDeleteUser, args)
	},
}

func adminCreateUser(config *conf.GlobalConfiguration, args []string) {
	db, err := storage.Dial(config)
	if err != nil {
		logrus.Fatalf("Error opening database: %+v", err)
	}
	defer db.Close()

	aud := getAudience(config)
	if user, err := models.IsDuplicatedEmail(db, args[0], aud, nil, config.Experimental.ProvidersWithOwnLinkingDomain); user != nil {
		logrus.Fatalf("Error creating new user: user already exists")
	} else if err != nil {
		logrus.Fatalf("Error checking user email: %+v", err)
	}

	user, err := models.NewUser("", args[0], args[1], aud, nil)
	if err != nil {
		logrus.Fatalf("Error creating new user: %+v", err)
	}

	err = db.Transaction(func(tx *storage.Connection) error {
		var terr error
		if terr = tx.Create(user); terr != nil {
			return terr
		}

		if len(args) > 2 {
			if terr = user.SetRole(tx, args[2]); terr != nil {
				return terr
			}
		} else if isAdmin {
			if terr = user.SetRole(tx, config.JWT.AdminGroupName); terr != nil {
				return terr
			}
		}

		if config.Mailer.Autoconfirm || autoconfirm {
			if terr = user.Confirm(tx); terr != nil {
				return terr
			}
		}
		return nil
	})
	if err != nil {
		logrus.Fatalf("Unable to create user (%s): %+v", args[0], err)
	}

	logrus.Infof("Created user: %s", args[0])
}

func adminDeleteUser(config *conf.GlobalConfiguration, args []string) {
	db, err := storage.Dial(config)
	if err != nil {
		logrus.Fatalf("Error opening database: %+v", err)
	}
	defer db.Close()

	if err := deleteUser(db, getAudience(config), args[0]); err != nil {
		logrus.Fatalf("Error removing user (%s): %+v", args[0], err)
	}

	logrus.Infof("Removed user: %s", args[0])
}

// deleteUser removes the user `emailOrID` names, an email in `aud` or a user
// id. Like the admin API, it refuses the project's last Studio admin.
func deleteUser(db *storage.Connection, aud, emailOrID string) error {
	user, err := models.FindUserByEmailAndAudience(db, emailOrID, aud)
	if err != nil {
		userID, perr := uuid.FromString(emailOrID)
		if perr != nil {
			return fmt.Errorf("no user has the email %q, and it is not a user id", emailOrID)
		}
		if user, err = models.FindUserByID(db, userID); err != nil {
			return fmt.Errorf("finding user %s: %w", userID, err)
		}
	}
	// In a transaction, so the Studio membership check, its removal, its audit
	// row and the delete stand or fall together.
	return db.Transaction(func(tx *storage.Connection) error {
		return tx.Destroy(user)
	})
}
