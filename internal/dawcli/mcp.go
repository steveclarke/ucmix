package dawcli

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/daw"
	"github.com/steveclarke/ucmix/internal/dawmcp"
)

// version is set at build time.
var version = "dev"

func newMCPCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server on stdio (for Claude Code: claude mcp add studio-pro-ucnet -- ucdaw mcp)",
		Long: "Runs an MCP server on stdin/stdout. Each tool call uses `ucdaw serve` when it\n" +
			"runs (fast) and connects to Studio Pro directly when it does not.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			server := dawmcp.NewServer(func() (daw.Engine, error) { return newEngine(o) }, version)
			return server.Run(cmd.Context(), &mcp.StdioTransport{})
		},
	}
}
