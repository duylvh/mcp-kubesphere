package options

import cliflag "k8s.io/component-base/cli/flag"

const (
	defaultHTTPAddress  = "127.0.0.1:47814"
	defaultEndpointPath = "/mcp"
)

type HTTPOptions struct {
	KSConfig     string
	KSApiserver  string
	Address      string
	EndpointPath string
}

func NewHTTPOptions() *HTTPOptions {
	return &HTTPOptions{Address: defaultHTTPAddress, EndpointPath: defaultEndpointPath}
}

func (o *HTTPOptions) Flags() cliflag.NamedFlagSets {
	fss := cliflag.NamedFlagSets{}
	gfs := fss.FlagSet("generic")
	gfs.StringVar(&o.KSApiserver, "ks-apiserver", o.KSApiserver, "the kubesphere apiserver address.")
	gfs.StringVar(&o.KSConfig, "ksconfig", o.KSConfig, "the KubeSphere config file to access KubeSphere.")
	gfs.StringVar(&o.Address, "address", o.Address, "the loopback host:port to listen on.")
	gfs.StringVar(&o.EndpointPath, "endpoint-path", o.EndpointPath, "the path that serves MCP streamable HTTP.")

	return fss
}
