package datastore

import (
	"fmt"
	"io"
	"os"
	"path"
	"time"

	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/dataservices/errors"
	"github.com/sirupsen/logrus"
)

func (store *Store) version() (int, error) {
	version, err := store.VersionService.DBVersion()
	if store.IsErrObjectNotFound(err) {
		version = 0
	}
	return version, err
}

func (store *Store) edition() portainer.SoftwareEdition {
	edition, err := store.VersionService.Edition()
	if store.IsErrObjectNotFound(err) {
		edition = portainer.PortainerCE
	}
	return edition
}

// NewStore initializes a new Store and the associated services
func NewStore(storePath string, fileService portainer.FileService, connection portainer.Connection) *Store {
	return &Store{
		fileService: fileService,
		connection:  connection,
	}
}

// Open opens and initializes the BoltDB database.
func (store *Store) Open() (newStore bool, err error) {
	newStore = true

	err = store.connection.Open()
	if err != nil {
		return newStore, err
	}

	err = store.initServices()
	if err != nil {
		return newStore, err
	}

	// if we have DBVersion in the database then ensure we flag this as NOT a new store
	if _, err := store.VersionService.DBVersion(); err == nil {
		newStore = false
	}

	if !newStore && !store.connection.IsEncryptedStore() {
		logrus.Infof("The existing store is NOT encrypted")
		store.encryptDB()
	}

	return newStore, nil
}

func (store *Store) Close() error {
	return store.connection.Close()
}

// BackupTo backs up db to a provided writer.
// It does hot backup and doesn't block other database reads and writes
func (store *Store) BackupTo(w io.Writer) error {
	return store.connection.BackupTo(w)
}

// CheckCurrentEdition checks if current edition is community edition
func (store *Store) CheckCurrentEdition() error {
	if store.edition() != portainer.PortainerCE {
		return errors.ErrWrongDBEdition
	}
	return nil
}

// TODO: move the use of this to dataservices.IsErrObjectNotFound()?
func (store *Store) IsErrObjectNotFound(e error) bool {
	return e == errors.ErrObjectNotFound
}

func (store *Store) Rollback(force bool) error {
	return store.connectionRollback(force)
}

func (store *Store) encryptDB() error {
	// Since database is not encrypted so
	// settings this flag to false will not
	// allow connection to use encryption key

	oldFilename := store.connection.GetDatabaseFilename(true)
	logrus.Infof("Encrypting database")

	// export file path for backup
	exportFilename := path.Join(store.databasePath() + "." + fmt.Sprintf("backup-%d.json", time.Now().Unix()))

	logrus.Infof("Exporting database backup to %s", exportFilename)
	err := store.Export(exportFilename)
	if err != nil {
		logrus.WithError(err).Debugf("failed to export to %s", exportFilename)
		return err
	}

	logrus.Infof("database backup exported")

	// Set isDBEncryptedFlag to true to import JSON in the encrypted format
	store.connection.SetEncrypted(true)
	store.connection.Close() // So we can delete the old db file

	// Re-open the store as a new encrypted db file
	store.connection.Open()

	err = store.initServices()
	if err != nil {
		return err
	}

	err = store.Import(exportFilename)
	if err != nil {
		logrus.Fatal(errors.ErrDBImportFailed.Error())

		// Remove the new encrypted file that failed
		os.Remove(store.connection.GetDatabaseFilename(true))
	}

	err = os.Remove(oldFilename)
	if err != nil {
		logrus.Errorf("failed to remove the un-encrypted db file")
	}

	logrus.Info("Database successfully encrypted")
	return nil
}
