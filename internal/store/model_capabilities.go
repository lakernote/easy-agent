package store

import "time"

// RecordModelCapabilityTest stores only a one-way configuration fingerprint;
// provider credentials and request payloads remain in their existing stores.
func (store *Store) RecordModelCapabilityTest(fingerprint string, testedAt time.Time) error {
	_, err := store.db.Exec(`INSERT INTO ea_model_capability_tests(fingerprint,tested_at) VALUES(?,?)
ON CONFLICT(fingerprint) DO UPDATE SET tested_at=excluded.tested_at`, fingerprint, formatTime(testedAt))
	return err
}

func (store *Store) HasModelCapabilityTest(fingerprint string) (bool, error) {
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM ea_model_capability_tests WHERE fingerprint=?`, fingerprint).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}
