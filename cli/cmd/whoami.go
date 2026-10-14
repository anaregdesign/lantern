package cmd

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Read the authenticated user's Server-assigned ID",
	Long: `Read the current-v2 Principal's immutable user ID with the configured credential.

Prints {"user_id":"..."} or {"user_id":null} for an authenticated legacy UID-less
or machine Principal. Signed-out requests return an authentication error (exit 2).
No data or management Role is required. This does not register a user, infer an ID
from provider subject/email, or grant access to the user's personal data.

Example: LANTERN_TOKEN=<runtime-credential> lantern-cli --tls --address host:443 whoami`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		client, err := dial()
		if err != nil {
			return err
		}
		defer func() { _ = client.Close() }()
		id, err := client.GetCurrentUserID(cmd.Context())
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			UserID *string `json:"user_id"`
		}{id})
	},
}

func init() { rootCmd.AddCommand(whoamiCmd) }
