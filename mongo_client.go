package main

import (
	"github.com/sirupsen/logrus"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

var (
	mongoUri  string
	mgoClient *MongoClient
)

func IsUseMongo() bool {
	return mongoUri != ""
}

type MongoClient struct {
	db *mgo.Session
}

func NewMongoClient() *MongoClient {
	session, err := mgo.Dial(mongoUri)
	if err != nil {
		logrus.Fatalf("failed to init mongo client")
	}
	logrus.Info("init mongodb client finished.")
	return &MongoClient{session}
}

func (m *MongoClient) SaveTopics(topics []*Topic, timestamp int64) (err error) {
	coll := m.db.DB(defaultDName).C(collectTopics)
	for i := 0; i < len(topics); i++ {
		if err = coll.Insert(bson.M{
			"timestamp":         timestamp,
			"name":              topics[i].Name,
			"partitions":        topics[i].Partitions,
			"subscribers":       topics[i].Subscribers,
			"available_offsets": topics[i].AvailableOffsets,
			"logsize":           topics[i].LogSize,
		}); err != nil {
			return
		}
	}
	return
}

func (m *MongoClient) SaveSubscriber(subscriber []Subscriber, timestamp int64) (err error) {
	coll := m.db.DB(defaultDName).C(collectSubscribers)
	for i := 0; i < len(subscriber); i++ {
		_, err = coll.Upsert(
			bson.M{"group_id": subscriber[i].GroupID},
			bson.M{
				"timestamp": timestamp,
				"group_id":  subscriber[i].GroupID,
				"topics":    subscriber[i].Topic,
			},
		)
		if err != nil {
			return
		}
	}
	return
}

func (m *MongoClient) SaveBrokers(brokers Brokers, timestamp int64) error {
	_, err := m.db.DB(defaultDName).C(collectBrokers).Upsert(
		bson.M{"_id": defaultBrokerID},
		bson.M{
			"_id":        defaultBrokerID,
			"timestamp":  timestamp,
			"members":    brokers.Members,
			"controller": brokers.Controller,
		},
	)
	return err
}

func (m *MongoClient) PingMongo() {
	if m.db.Ping() != nil {
		logrus.Warnf("could not connect mongo")
		m.db.Refresh()
	}
}
