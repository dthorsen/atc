package gc

import (
	"net/http"
	"time"

	"code.cloudfoundry.org/lager"
	"github.com/concourse/atc/db"
	"github.com/concourse/baggageclaim"
	bclient "github.com/concourse/baggageclaim/client"
)

type volumeCollector struct {
	rootLogger                lager.Logger
	volumeFactory             db.VolumeFactory
	baggageclaimClientFactory BaggageclaimClientFactory
}

//go:generate counterfeiter . BaggageclaimClientFactory

type BaggageclaimClientFactory interface {
	NewClient(apiURL string, workerName string) bclient.Client
}

type baggageclaimClientFactory struct {
	dbWorkerFactory db.WorkerFactory
}

func NewBaggageclaimClientFactory(dbWorkerFactory db.WorkerFactory) BaggageclaimClientFactory {
	return &baggageclaimClientFactory{
		dbWorkerFactory: dbWorkerFactory,
	}
}

func (f *baggageclaimClientFactory) NewClient(apiURL string, workerName string) bclient.Client {
	return bclient.NewWithHTTPClient(apiURL, &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives:     true,
			ResponseHeaderTimeout: 1 * time.Minute,
		},
	})
}

func NewVolumeCollector(
	logger lager.Logger,
	volumeFactory db.VolumeFactory,
	baggageclaimClientFactory BaggageclaimClientFactory,
) Collector {
	return &volumeCollector{
		rootLogger:                logger,
		volumeFactory:             volumeFactory,
		baggageclaimClientFactory: baggageclaimClientFactory,
	}
}

func (vc *volumeCollector) Run() error {
	logger := vc.rootLogger.Session("run")

	logger.Debug("start")
	defer logger.Debug("done")

	var (
		creatingVolumes   []db.CreatingVolume
		createdVolumes    []db.CreatedVolume
		destroyingVolumes []db.DestroyingVolume
		err               error
	)

	orphanedCreatedVolumes, orphanedDestroyingVolumes, err := vc.volumeFactory.GetOrphanedVolumes()
	if err != nil {
		logger.Error("failed-to-get-orphaned-volumes", err)
		return err
	}

	if len(orphanedCreatedVolumes) > 0 || len(orphanedDestroyingVolumes) > 0 {
		logger.Debug("found-orphaned-volumes", lager.Data{
			"created":    len(orphanedCreatedVolumes),
			"destroying": len(orphanedDestroyingVolumes),
		})
	}

	createdVolumes = append(createdVolumes, orphanedCreatedVolumes...)
	destroyingVolumes = append(destroyingVolumes, orphanedDestroyingVolumes...)

	for _, creatingVolume := range creatingVolumes {
		vLog := logger.Session("mark-creating-as-created", lager.Data{
			"volume": creatingVolume.Handle(),
		})

		createdVolume, err := creatingVolume.Created()
		if err != nil {
			vLog.Error("failed-to-transition-from-creating-to-created", err)
			continue
		}

		createdVolumes = append(createdVolumes, createdVolume)
	}

	for _, createdVolume := range createdVolumes {
		vLog := logger.Session("mark-created-as-destroying", lager.Data{
			"volume": createdVolume.Handle(),
			"worker": createdVolume.Worker().Name(),
		})

		destroyingVolume, err := createdVolume.Destroying()
		if err != nil {
			vLog.Error("failed-to-transition", err)
			continue
		}

		destroyingVolumes = append(destroyingVolumes, destroyingVolume)
	}

	for _, destroyingVolume := range destroyingVolumes {
		vLog := logger.Session("destroy", lager.Data{
			"handle": destroyingVolume.Handle(),
			"worker": destroyingVolume.Worker().Name(),
		})

		if destroyingVolume.Worker().BaggageclaimURL() == nil {
			vLog.Info("baggageclaim-url-is-missing")
			continue
		}

		baggageclaimClient := vc.baggageclaimClientFactory.NewClient(*destroyingVolume.Worker().BaggageclaimURL(), destroyingVolume.Worker().Name())

		volume, found, err := baggageclaimClient.LookupVolume(vLog, destroyingVolume.Handle())
		if err != nil {
			vLog.Error("failed-to-lookup-volume-in-baggageclaim", err)
			continue
		}

		if vc.destroyRealVolume(vLog.Session("in-worker"), volume, found) {
			vc.destroyDBVolume(vLog.Session("in-db"), destroyingVolume)
		}
	}

	return nil
}

func (vc *volumeCollector) destroyRealVolume(logger lager.Logger, volume baggageclaim.Volume, found bool) bool {
	if found {
		logger.Debug("destroying")

		err := volume.Destroy()
		if err != nil {
			logger.Error("failed-to-destroy", err)
			return false
		}

		logger.Debug("destroyed")
	} else {
		logger.Debug("already-removed")
	}

	return true
}

func (vc *volumeCollector) destroyDBVolume(logger lager.Logger, dbVolume db.DestroyingVolume) {
	logger.Debug("destroying")

	destroyed, err := dbVolume.Destroy()
	if err != nil {
		logger.Error("failed-to-destroy", err)
		return
	}

	if !destroyed {
		logger.Info("could-not-destroy")
		return
	}

	logger.Debug("destroyed")
}
