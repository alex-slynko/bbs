package encryptor

import (
	"errors"
	"os"

	"code.cloudfoundry.org/bbs/db"
	"code.cloudfoundry.org/bbs/encryption"
	"code.cloudfoundry.org/bbs/models"
	"code.cloudfoundry.org/clock"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/runtimeschema/metric"
)

const (
	encryptionDuration = metric.Duration("EncryptionDuration")
)

type Encryptor struct {
	logger     lager.Logger
	db         db.EncryptionDB
	keyManager encryption.KeyManager
	cryptor    encryption.Cryptor
	clock      clock.Clock
}

func New(
	logger lager.Logger,
	db db.EncryptionDB,
	keyManager encryption.KeyManager,
	cryptor encryption.Cryptor,
	clock clock.Clock,
) Encryptor {
	return Encryptor{
		logger:     logger,
		db:         db,
		keyManager: keyManager,
		cryptor:    cryptor,
		clock:      clock,
	}
}

func (m Encryptor) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	logger := m.logger.Session("encryptor")
	logger.Info("starting")
	defer logger.Info("exited")

	currentEncryptionKey, err := m.db.EncryptionKeyLabel(logger)
	if err != nil {
		if models.ConvertError(err) != models.ErrResourceNotFound {
			logger.Error("failed-to-fetch-encryption-key-label", err)
			return err
		}
	} else {
		if m.keyManager.DecryptionKey(currentEncryptionKey) == nil {
			err := errors.New("Existing encryption key version (" + currentEncryptionKey + ") is not among the known keys")
			logger.Error("unknown-encryption-key-lable", err)
			return err
		}
	}

	close(ready)

	if currentEncryptionKey != m.keyManager.EncryptionKey().Label() {
		logger := logger.WithData(lager.Data{
			"desired-key-label":  m.keyManager.EncryptionKey().Label(),
			"existing-key-label": currentEncryptionKey,
		})

		encryptionStart := m.clock.Now()
		logger.Info("encryption-started")
		err := m.db.PerformEncryption(logger)
		if err != nil {
			logger.Error("encryption-failed", err)
		} else {
			m.db.SetEncryptionKeyLabel(logger, m.keyManager.EncryptionKey().Label())
		}

		totalTime := m.clock.Since(encryptionStart)
		logger.Info("encryption-finished", lager.Data{"total_time": totalTime})
		err = encryptionDuration.Send(totalTime)
		if err != nil {
			logger.Error("failed-to-send-encryption-duration-metrics", err)
		}
	}

	<-signals
	return nil
}
