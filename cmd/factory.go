package cmd

import (
	"errors"
	"fmt"
	"path"

	boshblob "github.com/cloudfoundry/bosh-agent/blobstore"
	bosherr "github.com/cloudfoundry/bosh-agent/errors"
	boshlog "github.com/cloudfoundry/bosh-agent/logger"
	boshcmd "github.com/cloudfoundry/bosh-agent/platform/commands"
	boshsys "github.com/cloudfoundry/bosh-agent/system"
	boshuuid "github.com/cloudfoundry/bosh-agent/uuid"

	bmcomp "github.com/cloudfoundry/bosh-micro-cli/compile"
	bmconfig "github.com/cloudfoundry/bosh-micro-cli/config"
	bmindex "github.com/cloudfoundry/bosh-micro-cli/index"
	bmrelvalidation "github.com/cloudfoundry/bosh-micro-cli/release/validation"
	bmtar "github.com/cloudfoundry/bosh-micro-cli/tar"
	bmui "github.com/cloudfoundry/bosh-micro-cli/ui"
	bmworkspace "github.com/cloudfoundry/bosh-micro-cli/workspace"
)

type Factory interface {
	CreateCommand(name string) (Cmd, error)
}

type factory struct {
	commands      map[string](func() (Cmd, error))
	config        bmconfig.Config
	configService bmconfig.Service
	fileSystem    boshsys.FileSystem
	ui            bmui.UI
	logger        boshlog.Logger
	workspace     bmworkspace.Workspace
	uuidGenerator boshuuid.Generator
}

func NewFactory(
	config bmconfig.Config,
	configService bmconfig.Service,
	fileSystem boshsys.FileSystem,
	ui bmui.UI,
	logger boshlog.Logger,
	workspace bmworkspace.Workspace,
	uuidGenerator boshuuid.Generator,

) Factory {
	f := &factory{
		config:        config,
		configService: configService,
		fileSystem:    fileSystem,
		ui:            ui,
		logger:        logger,
		workspace:     workspace,
		uuidGenerator: uuidGenerator,
	}
	f.commands = map[string](func() (Cmd, error)){
		"deployment": f.createDeploymentCmd,
		"deploy":     f.createDeployCmd,
	}
	return f
}

func (f *factory) CreateCommand(name string) (Cmd, error) {
	if f.commands[name] == nil {
		return nil, errors.New("Invalid command name")
	}

	return f.commands[name]()
}

func (f *factory) createDeploymentCmd() (Cmd, error) {
	return NewDeploymentCmd(
		f.ui,
		f.config,
		f.configService,
		f.fileSystem,
		f.workspace,
		f.logger,
	), nil
}

func (f *factory) createDeployCmd() (Cmd, error) {
	manifestFilePath := f.config.Deployment
	err := f.workspace.Load(manifestFilePath)
	if err != nil {
		return nil, bosherr.WrapError(err, "Loading workspace")
	}

	runner := boshsys.NewExecCmdRunner(f.logger)
	extractor := bmtar.NewCmdExtractor(runner, f.logger)

	boshValidator := bmrelvalidation.NewBoshValidator(f.fileSystem)
	cpiReleaseValidator := bmrelvalidation.NewCpiValidator()
	releaseValidator := bmrelvalidation.NewValidator(
		boshValidator,
		cpiReleaseValidator,
		f.ui,
	)

	compressor := boshcmd.NewTarballCompressor(runner, f.fileSystem)
	indexFilePath := path.Join(f.workspace.MicroBoshPath(), "index.json")
	index := bmindex.NewFileIndex(indexFilePath, f.fileSystem)
	compiledPackageRepo := bmcomp.NewCompiledPackageRepo(index)

	f.logger.Debug(
		logTag,
		fmt.Sprintf("Creating new blobstore with path `%s'", f.workspace.BlobstorePath()),
	)
	options := map[string]interface{}{"blobstore_path": f.workspace.BlobstorePath()}
	blobstore := boshblob.NewSHA1VerifiableBlobstore(
		boshblob.NewLocalBlobstore(f.fileSystem, f.uuidGenerator, options),
	)
	packageCompiler := bmcomp.NewPackageCompiler(
		runner,
		f.workspace.PackagesPath(),
		f.fileSystem,
		compressor,
		blobstore,
		compiledPackageRepo,
		f.ui,
	)

	da := bmcomp.NewDependencyAnalysis()
	releaseCompiler := bmcomp.NewReleaseCompiler(da, packageCompiler)

	return NewDeployCmd(
		f.ui,
		f.config,
		f.fileSystem,
		extractor,
		releaseValidator,
		releaseCompiler,
		f.logger,
	), nil
}
