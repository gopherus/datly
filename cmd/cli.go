package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jessevdk/go-flags"
	"github.com/viant/afs"
	"github.com/viant/afs/file"
	"github.com/viant/afs/modifier"
	"github.com/viant/datly/auth/jwt"
	"github.com/viant/datly/gateway/runtime/standalone"
	"github.com/viant/datly/gateway/warmup"
	"github.com/viant/datly/router"
	"github.com/viant/datly/router/openapi3"
	"github.com/viant/datly/view"
	"gopkg.in/yaml.v3"
	"io"
	"os"
)

type serverBuilder struct {
	options    *Options
	connectors map[string]*view.Connector
	config     *standalone.Config
	logger     io.Writer
	route      *router.Resource
	fs         afs.Service
}

func (s *serverBuilder) build() (*standalone.Server, error) {
	ctx := context.Background()
	err := s.loadAndInitConfig(ctx)
	if err != nil {
		return nil, err
	}

	reportContent(s.logger, "------------ config ------------\n\t "+s.options.ConfigURL, s.options.ConfigURL)

	authenticator, err := jwt.Init(s.config.Config, nil)
	if authenticator != nil {
		fmt.Printf("with auth Service: %T\n", authenticator)
	}

	if URL := s.options.DepURL("connections"); URL != "" {
		reportContent(s.logger, "---------- connections: -----------\n\t"+URL, URL)
	}

	if URL := s.options.RouterURL(); URL != "" {
		reportContent(s.logger, "-------------- view --- -----------\n\t"+URL, URL)
	}
	if s.options.WriteLocation != "" {
		dumpConfiguration(s.options)
		return nil, nil
	}

	var srv *standalone.Server
	if authenticator == nil {
		srv, err = standalone.New(s.config)
	} else {
		srv, err = standalone.NewWithAuth(s.config, authenticator)
	}

	if len(s.options.WarmupURIs) > 0 {
		fmt.Printf("starting cache warmup for: %v\n", s.options.WarmupURIs)
		response := warmup.PreCache(srv.Service.PreCachables, s.options.WarmupURIs...)
		data, _ := json.Marshal(response)
		fmt.Printf("%s\n", data)
	}

	if err != nil {
		return nil, err
	}
	if s.options.OpenApiURL != "" {
		//TODO: add opeanpi3.Info to Config
		openapiSpec, _ := router.GenerateOpenAPI3Spec(openapi3.Info{}, srv.Routes()...)
		openApiMarshal, _ := yaml.Marshal(openapiSpec)
		_ = os.WriteFile(s.options.OpenApiURL, openApiMarshal, file.DefaultFileOsMode)
	}

	if err != nil {
		return nil, err
	}

	_, _ = s.logger.Write([]byte(fmt.Sprintf("starting endpoint: %v\n", s.config.Endpoint.Port)))
	return srv, nil
}

func (s *serverBuilder) loadAndInitConfig(ctx context.Context) error {
	aConfig, err := s.loadConfig(ctx)
	if err != nil {
		return err
	}

	err = s.initConfig(ctx, aConfig)
	if err != nil {
		return err
	}

	s.config = aConfig
	return nil
}

func newBuilder(options *Options, logger io.Writer) *serverBuilder {
	return &serverBuilder{
		options:    options,
		connectors: map[string]*view.Connector{},
		logger:     logger,
		fs:         afs.New(),
	}
}

func New(version string, args []string, logger io.Writer) (*standalone.Server, error) {
	os.Setenv("AWS_SDK_LOAD_CONFIG", "true")
	options := &Options{}
	_, err := flags.ParseArgs(options, args)

	if options.Version {
		fmt.Printf("Datly: version: %v\n", version)
		return nil, nil
	}

	if isOption("-h", args) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	options.Init()
	return newBuilder(options, logger).build()
}

func dumpConfiguration(options *Options) {
	fs := afs.New()
	destURL := normalizeURL(options.WriteLocation)
	os.MkdirAll(destURL, file.DefaultDirOsMode)
	srcURL := "mem://localhost/dev"
	fs.Copy(context.Background(), "mem://localhost/dev", destURL, modifier.Replace(map[string]string{
		srcURL: destURL,
	}))
}

func reportContent(logger io.Writer, message string, URL string) {
	_, _ = logger.Write([]byte(message))
	fs := afs.New()
	data, _ := fs.DownloadWithURL(context.Background(), URL)
	_, _ = logger.Write([]byte(fmt.Sprintf("%s\n", data)))
}

func isOption(key string, args []string) bool {
	for _, arg := range args {
		if arg == "-h" {
			return true
		}
	}
	return false
}
