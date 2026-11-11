package app

import (
	"fmt"
	"net/http"
	"os"

	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"

	"github.com/duylvh/mcp-kubesphere/cmd/app/options"
	"github.com/duylvh/mcp-kubesphere/pkg/kubesphere"
	"github.com/duylvh/mcp-kubesphere/pkg/tools"
	"github.com/duylvh/mcp-kubesphere/pkg/version"
)

func NewRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "mcp-kubesphere",
		Short:        "KubeSphere MCP Server",
		Version:      fmt.Sprint(version.Get()),
		SilenceUsage: true,
	}

	cmd.AddCommand(newVersionCommand())
	cmd.AddCommand(newStudioCommand())
	cmd.AddCommand(newSSECommand())
	cmd.AddCommand(newHTTPCommand())

	return cmd
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version of KubeSphere MCP Server",
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Println(version.Get())
		},
	}
}

func newStudioCommand() *cobra.Command {
	o := options.NewStdioOptions()
	cmd := &cobra.Command{
		Use:   "stdio",
		Short: "Start stdio server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newMCPServer(o.KSConfig, o.KSApiserver)
			if err != nil {
				return err
			}
			return server.ServeStdio(s)
		},
	}

	for _, f := range o.Flags().FlagSets {
		cmd.Flags().AddFlagSet(f)
	}
	return cmd
}

func newSSECommand() *cobra.Command {
	o := options.NewSSEOptions()
	cmd := &cobra.Command{
		Use:   "sse",
		Short: "Start sse server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newMCPServer(o.KSConfig, o.KSApiserver)
			if err != nil {
				return err
			}
			return server.NewSSEServer(s).Start(":" + o.Port)
		},
	}

	for _, f := range o.Flags().FlagSets {
		cmd.Flags().AddFlagSet(f)
	}
	return cmd
}

func newHTTPCommand() *cobra.Command {
	o := options.NewHTTPOptions()
	cmd := &cobra.Command{
		Use:   "http",
		Short: "Start a stateless streamable HTTP server on loopback, guarded by the bearer token in " + TokenFileEnvVar,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := loopbackOnly(o.Address); err != nil {
				return err
			}
			if _, err := loadToken(os.Getenv(TokenFileEnvVar)); err != nil {
				return err
			}
			s, err := newMCPServer(o.KSConfig, o.KSApiserver)
			if err != nil {
				return err
			}
			mux := http.NewServeMux()
			mux.Handle(o.EndpointPath, server.NewStreamableHTTPServer(s,
				server.WithStateLess(true), server.WithEndpointPath(o.EndpointPath)))
			return serveGuarded(o.Address, mux)
		},
	}

	for _, f := range o.Flags().FlagSets {
		cmd.Flags().AddFlagSet(f)
	}
	return cmd
}

// newMCPServer connects to KubeSphere with a browser session file
// (KS_COOKIE_FILE) when set, or with the ksconfig bearer token otherwise.
func newMCPServer(ksconfigPath, apiserver string) (*server.MCPServer, error) {
	opts, err := kubesphere.OptionsFromEnv(ksconfigPath, apiserver)
	if err != nil {
		return nil, err
	}
	ksconfig, err := kubesphere.NewKSConfig(opts)
	if err != nil {
		return nil, err
	}
	s := server.NewMCPServer("KubeSphere", version.Get().GitVersion,
		server.WithInstructions("Read-only view of KubeSphere as the signed-in user; nothing here can change a cluster."))
	s.SetTools(tools.All(ksconfig)...)
	return s, nil
}
