package dbng

import (
	"encoding/json"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/concourse/atc"
)

type Pipeline struct {
	ID     int
	TeamID int
}

func (p *Pipeline) CreateJobBuild(tx Tx, jobName string) (*Build, error) {
	buildName, jobID, err := getNewBuildNameForJob(tx, jobName, p.ID)
	if err != nil {
		return nil, err
	}

	var buildID int
	err = psql.Insert("builds").
		Columns("name", "job_id", "team_id", "status", "manually_triggered").
		Values(buildName, jobID, p.TeamID, "pending", true).
		Suffix("RETURNING id").
		RunWith(tx).
		QueryRow().
		Scan(&buildID)
	if err != nil {
		// TODO: expicitly handle fkey constraint
		return nil, err
	}

	err = createBuildEventSeq(tx, buildID)
	if err != nil {
		return nil, err
	}

	return &Build{ID: buildID}, nil
}

func (p *Pipeline) SaveJob(tx Tx, job atc.JobConfig) error {
	configPayload, err := json.Marshal(job)
	if err != nil {
		return err
	}

	rows, err := psql.Update("jobs").
		Set("config", configPayload).
		Set("active", true).
		Where(sq.Eq{
			"name":        job.Name,
			"pipeline_id": p.ID,
		}).
		RunWith(tx).
		Exec()
	if err != nil {
		return err
	}

	affected, err := rows.RowsAffected()
	if err != nil {
		return err
	}

	if affected == 0 {
		_, err := psql.Insert("jobs").
			Columns("name", "pipeline_id", "config", "active").
			Values(job.Name, p.ID, configPayload, true).
			RunWith(tx).
			Exec()
		if err != nil {
			// TODO: handle unique violation err
			return err
		}
	}

	return nil
}

func getNewBuildNameForJob(tx Tx, jobName string, pipelineID int) (string, int, error) {
	var buildName string
	var jobID int
	err := tx.QueryRow(`
		UPDATE jobs
		SET build_number_seq = build_number_seq + 1
		WHERE name = $1 AND pipeline_id = $2
		RETURNING build_number_seq, id
	`, jobName, pipelineID).Scan(&buildName, &jobID)
	return buildName, jobID, err
}

func createBuildEventSeq(tx Tx, buildID int) error {
	_, err := tx.Exec(fmt.Sprintf(`
		CREATE SEQUENCE %s MINVALUE 0
	`, buildEventSeq(buildID)))
	return err
}

func buildEventSeq(buildID int) string {
	return fmt.Sprintf("build_event_id_seq_%d", buildID)
}