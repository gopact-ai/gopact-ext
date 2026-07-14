package dbstore

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	errRunKindConflict = errors.New("dbstore: run retention kind conflict")
	errRunRetired      = errors.New("dbstore: run has been retired")
)

func (store *Store) ensureRunRegistry(tx *gorm.DB, runID, kind string) (runRegistryRow, error) {
	row := runRegistryRow{
		RunID: runID, Kind: kind, State: runStateActive,
		UpdatedAtUnixNano: store.nowUTC().UnixNano(),
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return runRegistryRow{}, err
	}
	current, found, err := store.lockRunRegistry(tx, runID)
	if err != nil {
		return runRegistryRow{}, err
	}
	if !found {
		return runRegistryRow{}, errors.New("dbstore: run registry row disappeared")
	}
	if current.State != runStateActive {
		return runRegistryRow{}, errRunRetired
	}
	// Plain Append is also used when a workflow's journal and checkpointer are
	// separate Store instances, so journal writes may join an existing workflow
	// domain. A journal domain can never be promoted implicitly to workflow.
	if current.Kind != kind && !(kind == runKindJournal && current.Kind == runKindWorkflow) {
		return runRegistryRow{}, errRunKindConflict
	}
	return current, nil
}

func (store *Store) lockRunRegistry(tx *gorm.DB, runID string) (runRegistryRow, bool, error) {
	if store.dialect == "sqlite" {
		result := tx.Exec(
			`UPDATE `+registryTable+` SET updated_at_unix_nano = updated_at_unix_nano WHERE run_id = ?`,
			runID,
		)
		if result.Error != nil || result.RowsAffected != 1 {
			return runRegistryRow{}, false, result.Error
		}
		var row runRegistryRow
		if err := tx.Where("run_id = ?", runID).Take(&row).Error; err != nil {
			return runRegistryRow{}, false, err
		}
		return row, true, nil
	}
	var row runRegistryRow
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("run_id = ?", runID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return runRegistryRow{}, false, nil
	}
	return row, err == nil, err
}
