// Package contrast defines the local face-feature repository contract.
package database

const (
	StudentIDLength         = 10
	FeatureLength           = 512
	FaceSimilarityThreshold = 0.8
)

type Person struct {
	ID       int64
	PersonID string
	Name     string
	Feature  []float32
}

type Database interface {
	AddPerson(person *Person) error
	DeletePerson(personID string) error
	SearchPerson(person *Person) (*Person, error)
	SearchByFeature(feature []float32) (*Person, error)
	Close() error
}
