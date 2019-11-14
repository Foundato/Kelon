package data

import (
	"github.com/Foundato/kelon/configs"
	"github.com/Foundato/kelon/pkg/data"
	log "github.com/sirupsen/logrus"
)

func MakeDatastores(config *configs.DatastoreConfig) map[string]*data.Datastore {
	result := make(map[string]*data.Datastore)
	for dsName, ds := range config.Datastores {
		if ds.Type == "postgres" || ds.Type == "mysql" {
			newDs := NewSQLDatastore()
			log.Infof("Init SqlDatastore of type [%s] with alias [%s]", ds.Type, dsName)
			result[dsName] = &newDs
		} else {
			newDs := NewMongoDatastore()
			log.Infof("Init MongoDatastore of type [%s] with alias [%s]", ds.Type, dsName)
			result[dsName] = &newDs
		}
	}
	return result
}
