package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/pivotal-golang/lager"

	"github.com/concourse/atc"
	"github.com/concourse/atc/event"
)

type SQLDB struct {
	logger lager.Logger

	conn *sql.DB
	bus  *notificationsBus
}

const buildColumns = "id, name, job_name, status, scheduled, engine, engine_metadata, start_time, end_time"
const qualifiedBuildColumns = "b.id, b.name, b.job_name, b.status, b.scheduled, b.engine, b.engine_metadata, b.start_time, b.end_time"

func NewSQL(
	logger lager.Logger,
	sqldbConnection *sql.DB,
	listener *pq.Listener,
) *SQLDB {
	return &SQLDB{
		logger: logger,

		conn: sqldbConnection,
		bus:  newNotificationsBus(listener),
	}
}

func (db *SQLDB) GetConfig() (atc.Config, ConfigVersion, error) {
	var configBlob []byte
	var version int
	err := db.conn.QueryRow(`
		SELECT config, version
		FROM pipelines
	`).Scan(&configBlob, &version)
	if err != nil {
		if err == sql.ErrNoRows {
			return atc.Config{}, 0, nil
		} else {
			return atc.Config{}, 0, err
		}
	}

	var config atc.Config
	err = json.Unmarshal(configBlob, &config)
	if err != nil {
		return atc.Config{}, 0, err
	}

	return config, ConfigVersion(version), nil
}

func (db *SQLDB) SaveConfig(config atc.Config, from ConfigVersion) error {
	payload, err := json.Marshal(config)
	if err != nil {
		return err
	}

	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}

	defer tx.Rollback()

	var existingConfig int
	err = tx.QueryRow(`
		SELECT COUNT(1)
		FROM pipelines
	`).Scan(&existingConfig)
	if err != nil {
		return err
	}

	result, err := tx.Exec(`
		UPDATE pipelines
		SET config = $1, version = nextval('config_version_seq')
		WHERE version = $2
	`, payload, from)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		if existingConfig == 0 {
			_, err := tx.Exec(`
			INSERT INTO pipelines (config, version)
			VALUES ($1, nextval('config_version_seq'))
		`, payload)
			if err != nil {
				return err
			}
		} else {
			return ErrConfigComparisonFailed
		}
	}

	return tx.Commit()
}

func (db *SQLDB) CreatePipe(pipeGUID string, url string) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}

	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO pipes(id, url)
		VALUES ($1, $2)
	`, pipeGUID, url)

	if err != nil {
		return err
	}
	err = tx.Commit()
	if err != nil {
		return err
	}

	return nil
}

func (db *SQLDB) GetPipe(pipeGUID string) (Pipe, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return Pipe{}, err
	}

	defer tx.Rollback()

	var pipe Pipe

	err = tx.QueryRow(`
		SELECT id, coalesce(url, '') AS url
		FROM pipes
		WHERE id = $1
	`, pipeGUID).Scan(&pipe.ID, &pipe.URL)

	if err != nil {
		return Pipe{}, err
	}
	err = tx.Commit()
	if err != nil {
		return Pipe{}, err
	}

	return pipe, nil
}

func (db *SQLDB) GetAllJobBuilds(job string) ([]Build, error) {
	rows, err := db.conn.Query(`
		SELECT `+buildColumns+`
		FROM builds
		WHERE job_name = $1
		ORDER BY id DESC
	`, job)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	bs := []Build{}

	for rows.Next() {
		build, err := scanBuild(rows)
		if err != nil {
			return nil, err
		}

		bs = append(bs, build)
	}

	return bs, nil
}

func (db *SQLDB) GetAllBuilds() ([]Build, error) {
	rows, err := db.conn.Query(`
		SELECT ` + buildColumns + `
		FROM builds
		ORDER BY id DESC
	`)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	bs := []Build{}

	for rows.Next() {
		build, err := scanBuild(rows)
		if err != nil {
			return nil, err
		}

		bs = append(bs, build)
	}

	return bs, nil
}

func (db *SQLDB) GetAllStartedBuilds() ([]Build, error) {
	rows, err := db.conn.Query(`
		SELECT ` + buildColumns + `
		FROM builds
		WHERE status = 'started'
	`)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	bs := []Build{}

	for rows.Next() {
		build, err := scanBuild(rows)
		if err != nil {
			return nil, err
		}

		bs = append(bs, build)
	}

	return bs, nil
}

func (db *SQLDB) GetBuild(buildID int) (Build, error) {
	return scanBuild(db.conn.QueryRow(`
		SELECT `+buildColumns+`
		FROM builds
		WHERE id = $1
	`, buildID))
}

func (db *SQLDB) GetJob(jobName string) (Job, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return Job{}, err
	}

	defer tx.Rollback()

	err = registerJob(tx, jobName)
	if err != nil {
		return Job{}, err
	}

	var job Job

	err = tx.QueryRow(`
				SELECT name, paused
				FROM jobs
				WHERE name = $1
			`, jobName).Scan(&job.Name, &job.Paused)
	if err != nil {
		return Job{}, err
	}

	err = tx.Commit()
	if err != nil {
		return Job{}, err
	}

	return job, nil
}

func (db *SQLDB) PauseJob(job string) error {
	return db.updatePausedJob(job, true)
}

func (db *SQLDB) UnpauseJob(job string) error {
	return db.updatePausedJob(job, false)
}

func (db *SQLDB) updatePausedJob(job string, pause bool) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}

	defer tx.Rollback()

	err = registerJob(tx, job)
	if err != nil {
		return err
	}

	result, err := tx.Exec(`
		UPDATE jobs
		SET paused = $1
		WHERE name = $2
	`, pause, job)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected != 1 {
		return nonOneRowAffectedError{rowsAffected}
	}

	return tx.Commit()
}

func (db *SQLDB) GetJobBuild(job string, name string) (Build, error) {
	return scanBuild(db.conn.QueryRow(`
		SELECT `+buildColumns+`
		FROM builds
		WHERE job_name = $1
		AND name = $2
	`, job, name))
}

func (db *SQLDB) GetResource(resourceName string) (Resource, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return Resource{}, err
	}

	defer tx.Rollback()

	err = registerResource(tx, resourceName)
	if err != nil {
		return Resource{}, err
	}

	var checkErr sql.NullString
	var resource Resource

	err = tx.QueryRow(`
		SELECT name, check_error, paused
		FROM resources
		WHERE name = $1
	`, resourceName).Scan(&resource.Name, &checkErr, &resource.Paused)
	if err != nil {
		return Resource{}, err
	}

	if checkErr.Valid {
		resource.CheckError = errors.New(checkErr.String)
	}

	err = tx.Commit()
	if err != nil {
		return Resource{}, err
	}

	return resource, nil
}

func (db *SQLDB) PauseResource(resource string) error {
	return db.updatePaused(resource, true)
}

func (db *SQLDB) UnpauseResource(resource string) error {
	return db.updatePaused(resource, false)
}

func (db *SQLDB) updatePaused(resource string, pause bool) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}

	defer tx.Rollback()

	err = registerResource(tx, resource)
	if err != nil {
		return err
	}

	result, err := tx.Exec(`
		UPDATE resources
		SET paused = $1
		WHERE name = $2
	`, pause, resource)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected != 1 {
		return nonOneRowAffectedError{rowsAffected}
	}

	return tx.Commit()
}

func (db *SQLDB) SetResourceCheckError(resourceName string, cause error) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}

	defer tx.Rollback()

	err = registerResource(tx, resourceName)
	if err != nil {
		return err
	}

	if cause == nil {
		_, err = tx.Exec(`
			UPDATE resources
			SET check_error = NULL
			WHERE name = $1
			`, resourceName)
	} else {
		_, err = tx.Exec(`
			UPDATE resources
			SET check_error = $2
			WHERE name = $1
		`, resourceName, cause.Error())
	}

	if err != nil {
		return err
	}

	return tx.Commit()
}

func (db *SQLDB) GetBuildResources(buildID int) ([]BuildInput, []BuildOutput, error) {
	inputs := []BuildInput{}
	outputs := []BuildOutput{}

	rows, err := db.conn.Query(`
		SELECT i.name, v.resource_name, v.type, v.source, v.version, v.metadata,
		NOT EXISTS (
			SELECT 1
			FROM build_inputs ci, builds cb
			WHERE versioned_resource_id = v.id
			AND cb.job_name = b.job_name
			AND ci.build_id = cb.id
			AND ci.build_id < b.id
		)
		FROM versioned_resources v, build_inputs i, builds b
		WHERE b.id = $1
		AND i.build_id = b.id
		AND i.versioned_resource_id = v.id
	`, buildID)
	if err != nil {
		return nil, nil, err
	}

	defer rows.Close()

	for rows.Next() {
		var inputName string
		var vr VersionedResource
		var firstOccurrence bool

		var source, version, metadata string
		err := rows.Scan(&inputName, &vr.Resource, &vr.Type, &source, &version, &metadata, &firstOccurrence)
		if err != nil {
			return nil, nil, err
		}

		err = json.Unmarshal([]byte(source), &vr.Source)
		if err != nil {
			return nil, nil, err
		}

		err = json.Unmarshal([]byte(version), &vr.Version)
		if err != nil {
			return nil, nil, err
		}

		err = json.Unmarshal([]byte(metadata), &vr.Metadata)
		if err != nil {
			return nil, nil, err
		}

		inputs = append(inputs, BuildInput{
			Name:              inputName,
			VersionedResource: vr,
			FirstOccurrence:   firstOccurrence,
		})
	}

	rows, err = db.conn.Query(`
		SELECT v.resource_name, v.type, v.source, v.version, v.metadata
		FROM versioned_resources v, build_outputs o, builds b
		WHERE b.id = $1
		AND o.build_id = b.id
		AND o.versioned_resource_id = v.id
		AND NOT EXISTS (
			SELECT 1
			FROM build_inputs
			WHERE versioned_resource_id = v.id
			AND build_id = b.id
		)
	`, buildID)
	if err != nil {
		return nil, nil, err
	}

	defer rows.Close()

	for rows.Next() {
		var vr VersionedResource

		var source, version, metadata string
		err := rows.Scan(&vr.Resource, &vr.Type, &source, &version, &metadata)
		if err != nil {
			return nil, nil, err
		}

		err = json.Unmarshal([]byte(source), &vr.Source)
		if err != nil {
			return nil, nil, err
		}

		err = json.Unmarshal([]byte(version), &vr.Version)
		if err != nil {
			return nil, nil, err
		}

		err = json.Unmarshal([]byte(metadata), &vr.Metadata)
		if err != nil {
			return nil, nil, err
		}

		outputs = append(outputs, BuildOutput{
			VersionedResource: vr,
		})
	}

	return inputs, outputs, nil
}

func (db *SQLDB) GetCurrentBuild(job string) (Build, error) {
	rows, err := db.conn.Query(`
		SELECT `+buildColumns+`
		FROM builds
		WHERE job_name = $1
		AND status != 'pending'
		ORDER BY id DESC
		LIMIT 1
	`, job)
	if err != nil {
		return Build{}, err
	}

	defer rows.Close()

	if rows.Next() {
		return scanBuild(rows)
	}

	pendingRows, err := db.conn.Query(`
			SELECT `+buildColumns+`
			FROM builds
			WHERE job_name = $1
			AND status = 'pending'
			ORDER BY id ASC
			LIMIT 1
		`, job)
	if err != nil {
		return Build{}, err
	}

	defer pendingRows.Close()

	if pendingRows.Next() {
		return scanBuild(pendingRows)
	}

	return Build{}, ErrNoBuild
}

func (db *SQLDB) GetJobFinishedAndNextBuild(job string) (*Build, *Build, error) {
	var finished *Build
	var next *Build

	finishedBuild, err := scanBuild(db.conn.QueryRow(`
		SELECT `+buildColumns+`
		FROM builds
		WHERE job_name = $1
		AND status NOT IN ('pending', 'started')
		ORDER BY id DESC
		LIMIT 1
	`, job))
	if err == nil {
		finished = &finishedBuild
	} else if err != nil && err != ErrNoBuild {
		return nil, nil, err
	}

	nextBuild, err := scanBuild(db.conn.QueryRow(`
		SELECT `+buildColumns+`
		FROM builds
		WHERE job_name = $1
		AND status IN ('pending', 'started')
		ORDER BY id ASC
		LIMIT 1
	`, job))
	if err == nil {
		next = &nextBuild
	} else if err != nil && err != ErrNoBuild {
		return nil, nil, err
	}

	return finished, next, nil
}

func (db *SQLDB) CreateJobBuild(job string) (Build, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return Build{}, err
	}

	defer tx.Rollback()

	err = registerJob(tx, job)
	if err != nil {
		return Build{}, err
	}

	var name string
	err = tx.QueryRow(`
		UPDATE jobs
		SET build_number_seq = build_number_seq + 1
		WHERE name = $1
		RETURNING build_number_seq
	`, job).Scan(&name)
	if err != nil {
		return Build{}, err
	}

	build, err := scanBuild(tx.QueryRow(`
		INSERT INTO builds (name, job_name, status)
		VALUES ($1, $2, 'pending')
		RETURNING `+buildColumns+`
	`, name, job))
	if err != nil {
		return Build{}, err
	}

	_, err = tx.Exec(fmt.Sprintf(`
		CREATE SEQUENCE %s MINVALUE 0
	`, buildEventSeq(build.ID)))
	if err != nil {
		return Build{}, err
	}

	err = tx.Commit()
	if err != nil {
		return Build{}, err
	}

	return build, nil
}

func (db *SQLDB) CreateOneOffBuild() (Build, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return Build{}, err
	}

	defer tx.Rollback()

	build, err := scanBuild(tx.QueryRow(`
		INSERT INTO builds (name, status)
		VALUES (nextval('one_off_name'), 'pending')
		RETURNING ` + buildColumns + `
	`))
	if err != nil {
		return Build{}, err
	}

	_, err = tx.Exec(fmt.Sprintf(`
		CREATE SEQUENCE %s MINVALUE 0
	`, buildEventSeq(build.ID)))
	if err != nil {
		return Build{}, err
	}

	err = tx.Commit()
	if err != nil {
		return Build{}, err
	}

	return build, nil
}

func (db *SQLDB) updateSerialGroupsForJob(jobName string, serialGroups []string) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}

	defer tx.Rollback()

	_, err = tx.Exec(`
		DELETE FROM jobs_serial_groups
		WHERE job_name = $1
	`, jobName)
	if err != nil {
		return err
	}

	for _, serialGroup := range serialGroups {
		_, err = tx.Exec(`
			INSERT INTO jobs_serial_groups (job_name, serial_group)
			VALUES ($1, $2)
		`, jobName, serialGroup)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (db *SQLDB) GetNextPendingBuildBySerialGroup(jobName string, serialGroups []string) (Build, error) {
	db.updateSerialGroupsForJob(jobName, serialGroups)

	serialGroupNames := []interface{}{}
	refs := []string{}
	for i, serialGroup := range serialGroups {
		serialGroupNames = append(serialGroupNames, serialGroup)
		refs = append(refs, fmt.Sprintf("$%d", i+1))
	}

	build, err := scanBuild(db.conn.QueryRow(`
		SELECT `+qualifiedBuildColumns+`
		FROM builds b
		INNER JOIN jobs j ON b.job_name = j.name
		INNER JOIN jobs_serial_groups jsg ON j.name = jsg.job_name
				AND jsg.serial_group IN (`+strings.Join(refs, ",")+`)
		WHERE b.status = 'pending'
		GROUP BY `+qualifiedBuildColumns+`
		ORDER BY id ASC
		LIMIT 1
	`, serialGroupNames...))

	if err != nil {
		return Build{}, err
	}

	return build, nil

}

func (db *SQLDB) GetRunningBuildsBySerialGroup(jobName string, serialGroups []string) ([]Build, error) {
	db.updateSerialGroupsForJob(jobName, serialGroups)

	serialGroupNames := []interface{}{}
	refs := []string{}
	for i, serialGroup := range serialGroups {
		serialGroupNames = append(serialGroupNames, serialGroup)
		refs = append(refs, fmt.Sprintf("$%d", i+1))
	}

	rows, err := db.conn.Query(`
		SELECT `+qualifiedBuildColumns+`
		FROM builds b
		INNER JOIN jobs j ON b.job_name = j.name
		INNER JOIN jobs_serial_groups jsg ON j.name = jsg.job_name
				AND jsg.serial_group IN (`+strings.Join(refs, ",")+`)
		WHERE (
				b.status = 'started'
				OR
				(b.scheduled = true AND b.status = 'pending')
			)
		GROUP BY `+qualifiedBuildColumns+`
	`, serialGroupNames...)

	if err != nil {
		return nil, err
	}

	defer rows.Close()

	bs := []Build{}

	for rows.Next() {
		build, err := scanBuild(rows)
		if err != nil {
			return nil, err
		}

		bs = append(bs, build)
	}

	return bs, nil
}

func (db *SQLDB) UpdateBuildToScheduled(buildID int) (bool, error) {
	result, err := db.conn.Exec(`
			UPDATE builds
			SET scheduled = true
			WHERE id = $1
	`, buildID)
	if err != nil {
		return false, err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	return rows == 1, nil
}

func (db *SQLDB) ScheduleBuild(buildID int, jobConfig atc.JobConfig) (bool, error) {
	build, err := db.GetBuild(buildID)
	if err != nil {
		return false, err
	}

	// The function needs to be idempotent, that's why this isn't in CanBuildBeScheduled
	if build.Scheduled {
		return true, nil
	}

	jobService, err := NewJobService(jobConfig, db)
	if err != nil {
		return false, err
	}

	canBuildBeScheduled, reason, err := jobService.CanBuildBeScheduled(build)
	if err != nil {
		return false, err
	}

	if canBuildBeScheduled {
		updated, err := db.UpdateBuildToScheduled(buildID)
		if err != nil {
			return false, err
		}

		return updated, nil
	} else {
		db.logger.Debug("Build did not schedule", lager.Data{
			"reason":  reason,
			"buildID": string(buildID),
		})
		return false, nil
	}
}

func (db *SQLDB) StartBuild(buildID int, engine, metadata string) (bool, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return false, err
	}

	defer tx.Rollback()

	var startTime time.Time

	err = tx.QueryRow(`
		UPDATE builds
		SET status = 'started', start_time = now(), engine = $2, engine_metadata = $3
		WHERE id = $1
		AND status = 'pending'
		RETURNING start_time
	`, buildID, engine, metadata).Scan(&startTime)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}

		return false, err
	}

	err = db.saveBuildEvent(tx, buildID, event.Status{
		Status: atc.StatusStarted,
		Time:   startTime.Unix(),
	})
	if err != nil {
		return false, err
	}

	err = tx.Commit()
	if err != nil {
		return false, err
	}

	// doesn't really need to be in transaction
	_, err = db.conn.Exec("NOTIFY " + buildEventsChannel(buildID))
	if err != nil {
		return false, err
	}

	return true, nil
}

func (db *SQLDB) FinishBuild(buildID int, status Status) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}

	defer tx.Rollback()

	var endTime time.Time

	err = tx.QueryRow(`
		UPDATE builds
		SET status = $2, end_time = now(), completed = true
		WHERE id = $1
		RETURNING end_time
	`, buildID, string(status)).Scan(&endTime)
	if err != nil {
		return err
	}

	err = db.saveBuildEvent(tx, buildID, event.Status{
		Status: atc.BuildStatus(status),
		Time:   endTime.Unix(),
	})
	if err != nil {
		return err
	}

	_, err = tx.Exec(fmt.Sprintf(`
		DROP SEQUENCE %s
	`, buildEventSeq(buildID)))
	if err != nil {
		return err
	}

	err = tx.Commit()
	if err != nil {
		return err
	}

	// doesn't really need to be in transaction
	_, err = db.conn.Exec("NOTIFY " + buildEventsChannel(buildID))
	if err != nil {
		return err
	}

	return nil
}

func (db *SQLDB) ErrorBuild(buildID int, cause error) error {
	err := db.SaveBuildEvent(buildID, event.Error{
		Message: cause.Error(),
	})
	if err != nil {
		return err
	}

	return db.FinishBuild(buildID, StatusErrored)
}

func (db *SQLDB) SaveBuildEngineMetadata(buildID int, engineMetadata string) error {
	_, err := db.conn.Exec(`
		UPDATE builds
		SET engine_metadata = $2
		WHERE id = $1
	`, buildID, engineMetadata)
	if err != nil {
		return err
	}

	return nil
}

func (db *SQLDB) SaveBuildInput(buildID int, input BuildInput) (SavedVersionedResource, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return SavedVersionedResource{}, err
	}

	defer tx.Rollback()

	svr, err := db.saveVersionedResource(tx, input.VersionedResource)
	if err != nil {
		return SavedVersionedResource{}, err
	}

	_, err = tx.Exec(`
		INSERT INTO build_inputs (build_id, versioned_resource_id, name)
		SELECT $1, $2, $3
		WHERE NOT EXISTS (
			SELECT 1
			FROM build_inputs
			WHERE build_id = $1
			AND versioned_resource_id = $2
			AND name = $3
		)
	`, buildID, svr.ID, input.Name)
	if err != nil {
		return SavedVersionedResource{}, err
	}

	err = tx.Commit()
	if err != nil {
		return SavedVersionedResource{}, err
	}

	return svr, nil
}

func (db *SQLDB) SaveBuildOutput(buildID int, vr VersionedResource) (SavedVersionedResource, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return SavedVersionedResource{}, err
	}

	defer tx.Rollback()

	svr, err := db.saveVersionedResource(tx, vr)
	if err != nil {
		return SavedVersionedResource{}, err
	}

	_, err = tx.Exec(`
		INSERT INTO build_outputs (build_id, versioned_resource_id)
		VALUES ($1, $2)
	`, buildID, svr.ID)
	if err != nil {
		return SavedVersionedResource{}, err
	}

	err = tx.Commit()
	if err != nil {
		return SavedVersionedResource{}, err
	}

	return svr, nil
}

func (db *SQLDB) GetBuildEvents(buildID int, from uint) (EventSource, error) {
	notifier, err := newConditionNotifier(db.bus, buildEventsChannel(buildID), func() (bool, error) {
		return true, nil
	})
	if err != nil {
		return nil, err
	}

	return newSQLDBBuildEventSource(
		buildID,
		db.conn,
		notifier,
		from,
	), nil
}

func (db *SQLDB) AbortBuild(buildID int) error {
	_, err := db.conn.Exec(`
		UPDATE builds
		SET status = 'aborted'
		WHERE id = $1
	`, buildID)
	if err != nil {
		return err
	}

	_, err = db.conn.Exec("NOTIFY " + buildAbortChannel(buildID))
	if err != nil {
		return err
	}

	return nil
}

func (db *SQLDB) AbortNotifier(buildID int) (Notifier, error) {
	return newConditionNotifier(db.bus, buildAbortChannel(buildID), func() (bool, error) {
		var aborted bool
		err := db.conn.QueryRow(`
			SELECT status = 'aborted'
			FROM builds
			WHERE id = $1
		`, buildID).Scan(&aborted)

		return aborted, err
	})
}

func (db *SQLDB) SaveBuildEvent(buildID int, event atc.Event) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}

	defer tx.Rollback()

	err = db.saveBuildEvent(tx, buildID, event)
	if err != nil {
		return err
	}

	err = tx.Commit()
	if err != nil {
		return err
	}

	// doesn't really need to be in transaction
	_, err = db.conn.Exec("NOTIFY " + buildEventsChannel(buildID))
	if err != nil {
		return err
	}

	return nil
}

func (db *SQLDB) SaveResourceVersions(config atc.ResourceConfig, versions []atc.Version) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}

	defer tx.Rollback()

	for _, version := range versions {
		_, err := db.saveVersionedResource(tx, VersionedResource{
			Resource: config.Name,
			Type:     config.Type,
			Source:   Source(config.Source),
			Version:  Version(version),
		})
		if err != nil {
			return err
		}
	}

	err = tx.Commit()
	if err != nil {
		return err
	}

	return nil
}

type nonOneRowAffectedError struct {
	RowsAffected int64
}

func (err nonOneRowAffectedError) Error() string {
	return fmt.Sprintf("expected 1 row to be updated; got %d", err.RowsAffected)
}

func (db *SQLDB) DisableVersionedResource(resourceID int) error {
	rows, err := db.conn.Exec(`
		UPDATE versioned_resources
		SET enabled = false
		WHERE id = $1
	`, resourceID)
	if err != nil {
		return err
	}

	rowsAffected, err := rows.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected != 1 {
		return nonOneRowAffectedError{rowsAffected}
	}

	return nil
}

func (db *SQLDB) EnableVersionedResource(resourceID int) error {
	rows, err := db.conn.Exec(`
		UPDATE versioned_resources
		SET enabled = true
		WHERE id = $1
	`, resourceID)
	if err != nil {
		return err
	}

	rowsAffected, err := rows.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected != 1 {
		return nonOneRowAffectedError{rowsAffected}
	}

	return nil
}

func (db *SQLDB) GetLatestVersionedResource(name string) (SavedVersionedResource, error) {
	var sourceBytes, versionBytes, metadataBytes string

	svr := SavedVersionedResource{
		VersionedResource: VersionedResource{
			Resource: name,
		},
	}

	err := db.conn.QueryRow(`
		SELECT id, enabled, type, source, version, metadata
		FROM versioned_resources
		WHERE resource_name = $1
		ORDER BY id DESC
		LIMIT 1
	`, name).Scan(&svr.ID, &svr.Enabled, &svr.Type, &sourceBytes, &versionBytes, &metadataBytes)
	if err != nil {
		return SavedVersionedResource{}, err
	}

	err = json.Unmarshal([]byte(sourceBytes), &svr.Source)
	if err != nil {
		return SavedVersionedResource{}, err
	}

	err = json.Unmarshal([]byte(versionBytes), &svr.Version)
	if err != nil {
		return SavedVersionedResource{}, err
	}

	err = json.Unmarshal([]byte(metadataBytes), &svr.Metadata)
	if err != nil {
		return SavedVersionedResource{}, err
	}

	return svr, nil
}

// buckle up
func (db *SQLDB) GetLatestInputVersions(inputs []atc.JobInput) ([]BuildInput, error) {
	fromAliases := []string{}
	conditions := []string{}
	params := []interface{}{}

	passedJobs := map[string]int{}

	for _, j := range inputs {
		params = append(params, j.Resource)
	}

	for i, j := range inputs {
		fromAliases = append(fromAliases, fmt.Sprintf("versioned_resources v%d", i+1))

		conditions = append(conditions, fmt.Sprintf("v%d.resource_name = $%d", i+1, i+1))

		for _, name := range j.Passed {
			idx, found := passedJobs[name]
			if !found {
				idx = len(passedJobs)
				passedJobs[name] = idx

				fromAliases = append(fromAliases, fmt.Sprintf("builds b%d", idx+1))

				conditions = append(conditions, fmt.Sprintf("b%d.job_name = $%d", idx+1, idx+len(inputs)+1))

				// add job name to params
				params = append(params, name)
			}

			fromAliases = append(fromAliases, fmt.Sprintf("build_outputs v%db%d", i+1, idx+1))

			conditions = append(conditions, fmt.Sprintf("v%db%d.versioned_resource_id = v%d.id", i+1, idx+1, i+1))

			conditions = append(conditions, fmt.Sprintf("v%db%d.build_id = b%d.id", i+1, idx+1, idx+1))
		}
	}

	buildInputs := []BuildInput{}

	for i, input := range inputs {
		svr := SavedVersionedResource{
			Enabled: true, // this is inherent with the following query
		}

		var source, version, metadata string

		err := db.conn.QueryRow(fmt.Sprintf(
			`
				SELECT v%[1]d.id, v%[1]d.resource_name, v%[1]d.type, v%[1]d.source, v%[1]d.version, v%[1]d.metadata
				FROM %s
				WHERE %s
				AND v%[1]d.enabled
				ORDER BY v%[1]d.id DESC
				LIMIT 1
			`,
			i+1,
			strings.Join(fromAliases, ", "),
			strings.Join(conditions, "\nAND "),
		), params...).Scan(&svr.ID, &svr.Resource, &svr.Type, &source, &version, &metadata)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, ErrNoVersions
			}

			return nil, err
		}

		params = append(params, svr.ID)
		conditions = append(conditions, fmt.Sprintf("v%d.id = $%d", i+1, len(params)))

		err = json.Unmarshal([]byte(source), &svr.Source)
		if err != nil {
			return nil, err
		}

		err = json.Unmarshal([]byte(version), &svr.Version)
		if err != nil {
			return nil, err
		}

		err = json.Unmarshal([]byte(metadata), &svr.Metadata)
		if err != nil {
			return nil, err
		}

		buildInputs = append(buildInputs, BuildInput{
			Name:              input.Name,
			VersionedResource: svr.VersionedResource,
		})
	}

	return buildInputs, nil
}

func (db *SQLDB) GetJobBuildForInputs(job string, inputs []BuildInput) (Build, error) {
	from := []string{"builds b"}
	conditions := []string{"job_name = $1"}
	params := []interface{}{job}

	for i, input := range inputs {
		vr := input.VersionedResource

		versionBytes, err := json.Marshal(vr.Version)
		if err != nil {
			return Build{}, err
		}

		var id int

		err = db.conn.QueryRow(`
			SELECT id
			FROM versioned_resources
			WHERE resource_name = $1
			AND type = $2
			AND version = $3
		`, vr.Resource, vr.Type, string(versionBytes)).Scan(&id)
		if err != nil {
			return Build{}, err
		}

		from = append(from, fmt.Sprintf("build_inputs i%d", i+1))
		params = append(params, id, input.Name)

		conditions = append(conditions,
			fmt.Sprintf("i%d.build_id = id", i+1),
			fmt.Sprintf("i%d.versioned_resource_id = $%d", i+1, len(params)-1),
			fmt.Sprintf("i%d.name = $%d", i+1, len(params)),
		)
	}

	return scanBuild(db.conn.QueryRow(fmt.Sprintf(`
		SELECT `+qualifiedBuildColumns+`
		FROM %s
		WHERE %s
		`,
		strings.Join(from, ", "),
		strings.Join(conditions, "\nAND ")),
		params...,
	))
}

func (db *SQLDB) CreateJobBuildWithInputs(job string, inputs []BuildInput) (Build, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return Build{}, err
	}

	defer tx.Rollback()

	err = registerJob(tx, job)
	if err != nil {
		return Build{}, err
	}

	var name string
	err = tx.QueryRow(`
		UPDATE jobs
		SET build_number_seq = build_number_seq + 1
		WHERE name = $1
		RETURNING build_number_seq
	`, job).Scan(&name)
	if err != nil {
		return Build{}, err
	}

	build, err := scanBuild(tx.QueryRow(`
		INSERT INTO builds (name, job_name, status)
		VALUES ($1, $2, 'pending')
		RETURNING `+buildColumns+`
	`, name, job))
	if err != nil {
		return Build{}, err
	}

	_, err = tx.Exec(fmt.Sprintf(`
		CREATE SEQUENCE %s MINVALUE 0
	`, buildEventSeq(build.ID)))
	if err != nil {
		return Build{}, err
	}

	for _, input := range inputs {
		svr, err := db.saveVersionedResource(tx, input.VersionedResource)
		if err != nil {
			return Build{}, err
		}

		_, err = tx.Exec(`
			INSERT INTO build_inputs (build_id, versioned_resource_id, name)
			VALUES ($1, $2, $3)
		`, build.ID, svr.ID, input.Name)
		if err != nil {
			return Build{}, err
		}
	}

	err = tx.Commit()
	if err != nil {
		return Build{}, err
	}

	return build, nil
}

func (db *SQLDB) GetNextPendingBuild(job string) (Build, []BuildInput, error) {
	build, err := scanBuild(db.conn.QueryRow(`
		SELECT `+buildColumns+`
		FROM builds
		WHERE job_name = $1
		AND status = 'pending'
		ORDER BY id ASC
		LIMIT 1
	`, job))
	if err != nil {
		return Build{}, nil, err
	}

	inputs, _, err := db.GetBuildResources(build.ID)
	if err != nil {
		return Build{}, nil, err
	}

	return build, inputs, nil
}

func (db *SQLDB) GetResourceHistory(resource string) ([]*VersionHistory, error) {
	hs := []*VersionHistory{}
	vhs := map[int]*VersionHistory{}

	inputHs := map[int]map[string]*JobHistory{}
	outputHs := map[int]map[string]*JobHistory{}
	seenInputs := map[int]map[int]bool{}

	vrRows, err := db.conn.Query(`
		SELECT v.id, v.enabled, v.resource_name, v.type, v.version, v.source, v.metadata
		FROM versioned_resources v
		WHERE v.resource_name = $1
		ORDER BY v.id DESC
	`, resource)
	if err != nil {
		return nil, err
	}

	defer vrRows.Close()

	for vrRows.Next() {
		var svr SavedVersionedResource

		var versionString, sourceString, metadataString string

		err := vrRows.Scan(&svr.ID, &svr.Enabled, &svr.Resource, &svr.Type, &versionString, &sourceString, &metadataString)
		if err != nil {
			return nil, err
		}

		err = json.Unmarshal([]byte(sourceString), &svr.Source)
		if err != nil {
			return nil, err
		}

		err = json.Unmarshal([]byte(versionString), &svr.Version)
		if err != nil {
			return nil, err
		}

		err = json.Unmarshal([]byte(metadataString), &svr.Metadata)
		if err != nil {
			return nil, err
		}

		vhs[svr.ID] = &VersionHistory{
			VersionedResource: svr,
		}

		hs = append(hs, vhs[svr.ID])

		inputHs[svr.ID] = map[string]*JobHistory{}
		outputHs[svr.ID] = map[string]*JobHistory{}
		seenInputs[svr.ID] = map[int]bool{}
	}

	for id, vh := range vhs {
		inRows, err := db.conn.Query(`
			SELECT `+qualifiedBuildColumns+`
			FROM builds b, build_inputs i
			WHERE i.versioned_resource_id = $1
			AND i.build_id = b.id
			ORDER BY b.id ASC
		`, id)
		if err != nil {
			return nil, err
		}

		defer inRows.Close()

		outRows, err := db.conn.Query(`
			SELECT `+qualifiedBuildColumns+`
			FROM builds b, build_outputs o
			WHERE o.versioned_resource_id = $1
			AND o.build_id = b.id
			ORDER BY b.id ASC
		`, id)
		if err != nil {
			return nil, err
		}

		defer outRows.Close()

		for inRows.Next() {
			inBuild, err := scanBuild(inRows)
			if err != nil {
				return nil, err
			}

			seenInputs[id][inBuild.ID] = true

			inputH, found := inputHs[id][inBuild.JobName]
			if !found {
				inputH = &JobHistory{
					JobName: inBuild.JobName,
				}

				vh.InputsTo = append(vh.InputsTo, inputH)

				inputHs[id][inBuild.JobName] = inputH
			}

			inputH.Builds = append(inputH.Builds, inBuild)
		}

		for outRows.Next() {
			outBuild, err := scanBuild(outRows)
			if err != nil {
				return nil, err
			}

			if seenInputs[id][outBuild.ID] {
				// don't show implicit outputs
				continue
			}

			outputH, found := outputHs[id][outBuild.JobName]
			if !found {
				outputH = &JobHistory{
					JobName: outBuild.JobName,
				}

				vh.OutputsOf = append(vh.OutputsOf, outputH)

				outputHs[id][outBuild.JobName] = outputH
			}

			outputH.Builds = append(outputH.Builds, outBuild)
		}
	}

	return hs, nil
}

func (db *SQLDB) acquireLock(lockType string, locks []NamedLock) (Lock, error) {
	params := []interface{}{}
	refs := []string{}
	for i, lock := range locks {
		params = append(params, lock.Name())
		refs = append(refs, fmt.Sprintf("$%d", i+1))

		_, err := db.conn.Exec(`
			INSERT INTO locks (name)
			VALUES ($1)
		`, lock.Name())
		if err != nil {
			if pqErr, ok := err.(*pq.Error); ok {
				if pqErr.Code.Class().Name() == "integrity_constraint_violation" {
					// unique violation is ok; no way to atomically upsert
					continue
				}
			}

			return nil, err
		}
	}

	tx, err := db.conn.Begin()
	if err != nil {
		return nil, err
	}

	result, err := tx.Exec(`
		SELECT 1 FROM locks
		WHERE name IN (`+strings.Join(refs, ",")+`)
		FOR `+lockType+`
	`, params...)
	if err != nil {
		tx.Commit()
		return nil, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		tx.Commit()
		return nil, err
	}

	if rowsAffected == 0 {
		tx.Commit()
		return nil, ErrLockRowNotPresentOrAlreadyDeleted
	}

	return &txLock{tx, db, locks}, nil
}

func (db *SQLDB) acquireLockLoop(lockType string, lock []NamedLock) (Lock, error) {
	for {
		lock, err := db.acquireLock(lockType, lock)
		if err != ErrLockRowNotPresentOrAlreadyDeleted {
			return lock, err
		}
	}
}

func (db *SQLDB) AcquireWriteLockImmediately(lock []NamedLock) (Lock, error) {
	return db.acquireLockLoop("UPDATE NOWAIT", lock)
}

func (db *SQLDB) AcquireWriteLock(lock []NamedLock) (Lock, error) {
	return db.acquireLockLoop("UPDATE", lock)
}

func (db *SQLDB) AcquireReadLock(lock []NamedLock) (Lock, error) {
	return db.acquireLockLoop("SHARE", lock)
}

func (db *SQLDB) ListLocks() ([]string, error) {
	rows, err := db.conn.Query("SELECT name FROM locks")
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	locks := []string{}

	for rows.Next() {
		var name string
		err := rows.Scan(&name)
		if err != nil {
			return nil, err
		}

		locks = append(locks, name)
	}

	return locks, nil
}

func (db *SQLDB) SaveWorker(info WorkerInfo, ttl time.Duration) error {
	resourceTypes, err := json.Marshal(info.ResourceTypes)
	if err != nil {
		return err
	}

	tags, err := json.Marshal(info.Tags)
	if err != nil {
		return err
	}

	if ttl == 0 {
		result, err := db.conn.Exec(`
			UPDATE workers
			SET expires = NULL, active_containers = $2, resource_types = $3, platform = $4, tags = $5
			WHERE addr = $1
		`, info.Addr, info.ActiveContainers, resourceTypes, info.Platform, tags)
		if err != nil {
			return err
		}

		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}

		if affected == 0 {
			_, err := db.conn.Exec(`
				INSERT INTO workers (addr, expires, active_containers, resource_types, platform, tags)
				VALUES ($1, NULL, $2, $3, $4, $5)
			`, info.Addr, info.ActiveContainers, resourceTypes, info.Platform, tags)
			if err != nil {
				return err
			}
		}

		return nil
	} else {
		interval := fmt.Sprintf("%d second", int(ttl.Seconds()))

		result, err := db.conn.Exec(`
			UPDATE workers
			SET expires = NOW() + $2::INTERVAL, active_containers = $3, resource_types = $4, platform = $5, tags = $6
			WHERE addr = $1
		`, info.Addr, interval, info.ActiveContainers, resourceTypes, info.Platform, tags)
		if err != nil {
			return err
		}

		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}

		if affected == 0 {
			_, err := db.conn.Exec(`
				INSERT INTO workers (addr, expires, active_containers, resource_types, platform, tags)
				VALUES ($1, NOW() + $2::INTERVAL, $3, $4, $5, $6)
			`, info.Addr, interval, info.ActiveContainers, resourceTypes, info.Platform, tags)
			if err != nil {
				return err
			}
		}

		return nil
	}
}

func (db *SQLDB) Workers() ([]WorkerInfo, error) {
	// reap expired workers
	_, err := db.conn.Exec(`
		DELETE FROM workers
		WHERE expires IS NOT NULL
		AND expires < NOW()
	`)
	if err != nil {
		return nil, err
	}

	// select remaining workers
	rows, err := db.conn.Query(`
		SELECT addr, active_containers, resource_types, platform, tags
		FROM workers
	`)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	infos := []WorkerInfo{}
	for rows.Next() {
		info := WorkerInfo{}

		var resourceTypes []byte
		var tags []byte

		err := rows.Scan(&info.Addr, &info.ActiveContainers, &resourceTypes, &info.Platform, &tags)
		if err != nil {
			return nil, err
		}

		err = json.Unmarshal(resourceTypes, &info.ResourceTypes)
		if err != nil {
			return nil, err
		}

		err = json.Unmarshal(tags, &info.Tags)
		if err != nil {
			return nil, err
		}

		infos = append(infos, info)
	}

	return infos, nil
}

type txLock struct {
	tx         *sql.Tx
	db         *SQLDB
	namedLocks []NamedLock
}

func (lock *txLock) release() error {
	return lock.tx.Commit()
}

func (lock *txLock) cleanup() error {
	lockNames := []interface{}{}
	refs := []string{}
	for i, l := range lock.namedLocks {
		lockNames = append(lockNames, l.Name())
		refs = append(refs, fmt.Sprintf("$%d", i+1))
	}

	cleanupLock, err := lock.db.acquireLock("UPDATE NOWAIT", lock.namedLocks)
	if err != nil {
		return nil
	}

	// acquireLock cannot return *txLock as that is a non-nil interface type when it fails
	internalLock := cleanupLock.(*txLock)

	_, err = internalLock.tx.Exec(`
		DELETE FROM locks
		WHERE name IN (`+strings.Join(refs, ",")+`)
	`, lockNames...)

	return internalLock.release()
}

func (lock *txLock) Release() error {
	err := lock.release()
	if err != nil {
		return err
	}

	return lock.cleanup()
}

func (db *SQLDB) saveBuildEvent(tx *sql.Tx, buildID int, event atc.Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}

	_, err = tx.Exec(fmt.Sprintf(`
		INSERT INTO build_events (event_id, build_id, type, version, payload)
		VALUES (nextval('%s'), $1, $2, $3, $4)
	`, buildEventSeq(buildID)), buildID, string(event.EventType()), string(event.Version()), payload)
	if err != nil {
		return err
	}

	return nil
}

func (db *SQLDB) saveVersionedResource(tx *sql.Tx, vr VersionedResource) (SavedVersionedResource, error) {
	err := registerResource(tx, vr.Resource)
	if err != nil {
		return SavedVersionedResource{}, err
	}

	versionJSON, err := json.Marshal(vr.Version)
	if err != nil {
		return SavedVersionedResource{}, err
	}

	sourceJSON, err := json.Marshal(vr.Source)
	if err != nil {
		return SavedVersionedResource{}, err
	}

	metadataJSON, err := json.Marshal(vr.Metadata)
	if err != nil {
		return SavedVersionedResource{}, err
	}

	var id int
	var enabled bool

	_, err = tx.Exec(`
		INSERT INTO versioned_resources (resource_name, type, version, source, metadata)
		SELECT $1, $2, $3, $4, $5
		WHERE NOT EXISTS (
			SELECT 1
			FROM versioned_resources
			WHERE resource_name = $1
			AND type = $2
			AND version = $3
		)
	`, vr.Resource, vr.Type, string(versionJSON), string(sourceJSON), string(metadataJSON))
	if err != nil {
		return SavedVersionedResource{}, err
	}

	// separate from above, as it conditionally inserts (can't use RETURNING)
	err = tx.QueryRow(`
		UPDATE versioned_resources
		SET source = $4, metadata = $5
		WHERE resource_name = $1
		AND type = $2
		AND version = $3
		RETURNING id, enabled
	`, vr.Resource, vr.Type, string(versionJSON), string(sourceJSON), string(metadataJSON)).Scan(&id, &enabled)

	if err != nil {
		return SavedVersionedResource{}, err
	}

	return SavedVersionedResource{
		ID:      id,
		Enabled: enabled,

		VersionedResource: vr,
	}, nil
}

type scannable interface {
	Scan(destinations ...interface{}) error
}

func scanBuild(row scannable) (Build, error) {
	var id int
	var name string
	var jobName sql.NullString
	var status string
	var scheduled bool
	var engine, engineMetadata sql.NullString
	var startTime pq.NullTime
	var endTime pq.NullTime

	err := row.Scan(&id, &name, &jobName, &status, &scheduled, &engine, &engineMetadata, &startTime, &endTime)
	if err != nil {
		if err == sql.ErrNoRows {
			return Build{}, ErrNoBuild
		}

		return Build{}, err
	}

	return Build{
		ID:        id,
		Name:      name,
		JobName:   jobName.String,
		Status:    Status(status),
		Scheduled: scheduled,

		Engine:         engine.String,
		EngineMetadata: engineMetadata.String,

		StartTime: startTime.Time,
		EndTime:   endTime.Time,
	}, nil
}

func registerJob(tx *sql.Tx, name string) error {
	_, err := tx.Exec(`
		INSERT INTO jobs (name)
		SELECT $1
		WHERE NOT EXISTS (
			SELECT 1 FROM jobs WHERE name = $1
		)
	`, name)
	return err
}

func registerResource(tx *sql.Tx, name string) error {
	_, err := tx.Exec(`
		INSERT INTO resources (name)
		SELECT $1
		WHERE NOT EXISTS (
			SELECT 1 FROM resources WHERE name = $1
		)
	`, name)
	return err
}

func buildEventsChannel(buildID int) string {
	return fmt.Sprintf("build_events_%d", buildID)
}

func buildAbortChannel(buildID int) string {
	return fmt.Sprintf("build_abort_%d", buildID)
}

func buildEventSeq(buildID int) string {
	return fmt.Sprintf("build_event_id_seq_%d", buildID)
}

func newConditionNotifier(bus *notificationsBus, channel string, cond func() (bool, error)) (Notifier, error) {
	notified, err := bus.Listen(channel)
	if err != nil {
		return nil, err
	}

	notifier := &conditionNotifier{
		cond:    cond,
		bus:     bus,
		channel: channel,

		notified: notified,
		notify:   make(chan struct{}, 1),

		stop: make(chan struct{}),
	}

	go notifier.watch()

	return notifier, nil
}

type conditionNotifier struct {
	cond func() (bool, error)

	bus     *notificationsBus
	channel string

	notified chan bool
	notify   chan struct{}

	stop chan struct{}
}

func (notifier *conditionNotifier) Notify() <-chan struct{} {
	return notifier.notify
}

func (notifier *conditionNotifier) Close() error {
	close(notifier.stop)
	return notifier.bus.Unlisten(notifier.channel, notifier.notified)
}

func (notifier *conditionNotifier) watch() {
	for {
		c, err := notifier.cond()
		if err != nil {
			select {
			case <-time.After(5 * time.Second):
				continue
			case <-notifier.stop:
				return
			}
		}

		if c {
			notifier.sendNotification()
		}

	dance:
		for {
			select {
			case <-notifier.stop:
				return
			case ok := <-notifier.notified:
				if ok {
					notifier.sendNotification()
				} else {
					break dance
				}
			}
		}
	}
}

func (notifier *conditionNotifier) sendNotification() {
	select {
	case notifier.notify <- struct{}{}:
	default:
	}
}
