package app

import (
	"fmt"

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
	s := server.NewMCPServer("KubeSphere", version.Get().GitVersion)
	s.SetTools(tools.All(ksconfig)...)
	return s, nil
}
