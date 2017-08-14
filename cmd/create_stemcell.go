package cmd

import (
	"os"
	"path/filepath"
	"time"

	bicloud "github.com/cloudfoundry/bosh-cli/cloud"
	biconfig "github.com/cloudfoundry/bosh-cli/config"
	bicpirel "github.com/cloudfoundry/bosh-cli/cpi/release"
	bideplmanifest "github.com/cloudfoundry/bosh-cli/deployment/manifest"
	bideplrel "github.com/cloudfoundry/bosh-cli/deployment/release"
	biinstall "github.com/cloudfoundry/bosh-cli/installation"
	biinstallmanifest "github.com/cloudfoundry/bosh-cli/installation/manifest"
	bitarball "github.com/cloudfoundry/bosh-cli/installation/tarball"
	biregistry "github.com/cloudfoundry/bosh-cli/registry"
	boshrel "github.com/cloudfoundry/bosh-cli/release"
	birelsetmanifest "github.com/cloudfoundry/bosh-cli/release/set/manifest"
	bistemcell "github.com/cloudfoundry/bosh-cli/stemcell"
	biui "github.com/cloudfoundry/bosh-cli/ui"
	bosherr "github.com/cloudfoundry/bosh-utils/errors"
	bihttpclient "github.com/cloudfoundry/bosh-utils/httpclient"
	boshlog "github.com/cloudfoundry/bosh-utils/logger"
)

type CreateStemcellCmd struct {
	ui     biui.UI
	deps   BasicDeps
	logger boshlog.Logger
	logTag string

	releaseSetAndInstallationManifestParser ReleaseSetAndInstallationManifestParser
	releaseManager                          biinstall.ReleaseManager
	cpiInstaller                            bicpirel.CpiInstaller
	workspaceRootPath                       string
	releaseFetcher                          biinstall.ReleaseFetcher
	tarballProvider                         bitarball.Provider
	tempRootConfigurator                    TempRootConfigurator
	cloudFactory                            bicloud.Factory
	stemcellExtractor                       bistemcell.Extractor
}

func NewCreateStemcellCmd(
	deps BasicDeps,
	logger boshlog.Logger,
) CreateStemcellCmd {
	releaseSetValidator := birelsetmanifest.NewValidator(deps.Logger)
	releaseSetParser := birelsetmanifest.NewParser(deps.FS, deps.Logger, releaseSetValidator)
	installValidator := biinstallmanifest.NewValidator(deps.Logger)
	installParser := biinstallmanifest.NewParser(deps.FS, deps.UUIDGen, deps.Logger, installValidator)
	releaseSetAndInstallationManifestParser := ReleaseSetAndInstallationManifestParser{
		ReleaseSetParser:   releaseSetParser,
		InstallationParser: installParser,
	}

	releaseManager := biinstall.NewReleaseManager(deps.Logger)
	releaseJobResolver := bideplrel.NewJobResolver(releaseManager)
	registryServer := biregistry.NewServerManager(deps.Logger)
	installerFactory := biinstall.NewInstallerFactory(
		deps.UI, deps.CmdRunner, deps.Compressor, releaseJobResolver,
		deps.UUIDGen, registryServer, deps.Logger, deps.FS, deps.DigestCreationAlgorithms,
	)

	cpiInstaller := bicpirel.CpiInstaller{
		ReleaseManager:   releaseManager,
		InstallerFactory: installerFactory,
		Validator:        bicpirel.NewValidator(),
	}

	workspaceRootPath := filepath.Join(os.Getenv("HOME"), ".bosh")
	tarballCacheBasePath := filepath.Join(workspaceRootPath, "downloads")
	tarballProvider := bitarball.NewProvider(
		bitarball.NewCache(tarballCacheBasePath, deps.FS, deps.Logger),
		deps.FS,
		bihttpclient.NewHTTPClient(bitarball.HTTPClient, deps.Logger),
		3,
		500*time.Millisecond,
		deps.Logger,
	)
	releaseProvider := boshrel.NewProvider(
		deps.CmdRunner, deps.Compressor, deps.DigestCalculator, deps.FS, deps.Logger,
	)
	releaseFetcher := biinstall.NewReleaseFetcher(
		tarballProvider,
		releaseProvider.NewExtractingArchiveReader(),
		releaseManager,
	)

	tempRootConfigurator := NewTempRootConfigurator(deps.FS)

	cloudFactory := bicloud.NewFactory(deps.FS, deps.CmdRunner, deps.Logger)

	stemcellReader := bistemcell.NewReader(deps.Compressor, deps.FS)
	stemcellExtractor := bistemcell.NewExtractor(stemcellReader, deps.FS)

	return CreateStemcellCmd{
		ui:     deps.UI,
		deps:   deps,
		logger: logger,
		logTag: "CreateStemcell",

		releaseSetAndInstallationManifestParser: releaseSetAndInstallationManifestParser,
		releaseManager:                          releaseManager,
		cpiInstaller:                            cpiInstaller,
		workspaceRootPath:                       workspaceRootPath,
		releaseFetcher:                          releaseFetcher,
		tarballProvider:                         tarballProvider,
		tempRootConfigurator:                    tempRootConfigurator,
		cloudFactory:                            cloudFactory,
		stemcellExtractor:                       stemcellExtractor,
	}
}

func (c CreateStemcellCmd) Run(stage biui.Stage, opts CreateStemcellOpts) error {
	manifestPath := opts.Manifest.Path
	statePath := opts.StatePath
	manifestVars := opts.VarFlags.AsVariables()
	manifestOp := opts.OpsFlags.AsOp()

	deploymentStateService := biconfig.NewFileSystemDeploymentStateService(
		c.deps.FS, c.deps.UUIDGen, c.deps.Logger, biconfig.DeploymentStatePath(manifestPath, statePath),
	)
	targetProvider := biinstall.NewTargetProvider(
		deploymentStateService,
		c.deps.UUIDGen,
		filepath.Join(c.workspaceRootPath, "installations"),
	)

	deploymentState, err := deploymentStateService.Load()
	if err != nil {
		return bosherr.WrapError(err, "Loading deployment state")
	}

	target, err := targetProvider.NewTarget()
	if err != nil {
		return bosherr.WrapError(err, "Determining installation target")
	}

	err = c.tempRootConfigurator.PrepareAndSetTempRoot(target.TmpPath(), c.logger)
	if err != nil {
		return bosherr.WrapError(err, "Setting temp root")
	}

	defer func() {
		err := c.releaseManager.DeleteAll()
		if err != nil {
			c.logger.Warn(c.logTag, "Deleting all extracted releases: %s", err.Error())
		}
	}()

	var (
		installationManifest biinstallmanifest.Manifest
		extractedStemcell    bistemcell.ExtractedStemcell
	)

	err = stage.PerformComplex("validating", func(stage biui.Stage) error {
		var releaseSetManifest birelsetmanifest.Manifest
		releaseSetManifest, installationManifest, err = c.releaseSetAndInstallationManifestParser.ReleaseSetAndInstallationManifest(
			manifestPath,
			manifestVars,
			manifestOp,
		)
		if err != nil {
			return err
		}

		cpiReleaseName := installationManifest.Template.Release
		cpiReleaseRef, found := releaseSetManifest.FindByName(cpiReleaseName)
		if !found {
			return bosherr.Errorf("installation release '%s' must refer to a release in releases", cpiReleaseName)
		}

		err = c.releaseFetcher.DownloadAndExtract(cpiReleaseRef, stage)
		if err != nil {
			return err
		}

		err = c.cpiInstaller.ValidateCpiRelease(installationManifest, stage)
		if err != nil {
			return err
		}

		return nil
	})

	return c.cpiInstaller.WithInstalledCpiRelease(installationManifest, target, stage, func(localCpiInstallation biinstall.Installation) error {
		var stemcellCid string
		err = stage.PerformComplex("creating stemcell", func(creatingStage biui.Stage) error {
			stemcell := bideplmanifest.StemcellRef{
				URL:  "https://bosh.io/d/stemcells/bosh-google-kvm-ubuntu-trusty-go_agent?v=3312.12",
				SHA1: "3a2c407be6c1b3d04bb292ceb5007159100c85d7",
			}

			stemcellTarballPath, err := c.tarballProvider.Get(stemcell, creatingStage)
			if err != nil {
				return err
			}

			err = creatingStage.Perform("Validating stemcell", func() error {
				extractedStemcell, err = c.stemcellExtractor.Extract(stemcellTarballPath)
				if err != nil {
					return bosherr.WrapErrorf(err, "Extracting stemcell from '%s'", stemcellTarballPath)
				}

				return nil
			})
			if err != nil {
				return bosherr.WrapError(err, "Validating stemcell")
			}

			defer func() {
				deleteErr := extractedStemcell.Cleanup()
				if deleteErr != nil {
					c.logger.Warn(c.logTag, "Failed to delete extracted stemcell: %s", deleteErr.Error())
				}
			}()

			cloud, err := c.cloudFactory.NewCloud(localCpiInstallation, deploymentState.DirectorID)
			if err != nil {
				return bosherr.WrapError(err, "Creating CPI client from CPI installation")
			}

			return creatingStage.Perform("Creating stemcell", func() error {
				stemcellCid, err = cloud.CreateStemcell(extractedStemcell.GetExtractedPath()+"/image", extractedStemcell.Manifest().CloudProperties)
				return err
			})
		})
		if err != nil {
			return bosherr.WrapError(err, "Creating stemcell")
		}

		c.deps.UI.PrintLinef("Created stemcell with cid '%s'", stemcellCid)

		return nil
	})
}
