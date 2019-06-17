package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Shopify/sarama"
	"github.com/sirupsen/logrus"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

const (
	collectTopics      = "topics"
	collectSubscribers = "subscribers"
	collectBrokers     = "brokers"

	defaultDName      = "kfk"
	defaultBrokerID   = 1
	defaultInterval   = 15
	defaultBrokerAddr = "localhost:9092"

	envBrokerAddr   = "BROKER_ADDR"
	envMongoUri     = "MONGO_URI"
	envTickInterval = "TICK_INTERVAL"
)

var (
	brokerAddr   = defaultBrokerAddr
	tickInterval = defaultInterval
	mongoUri     string
	mgoClient    *MongoClient
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
			"next_offsets":      topics[i].NextOffsets,
			"offset":            topics[i].Offsets,
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

func init() {
	if os.Getenv(envBrokerAddr) != "" {
		brokerAddr = os.Getenv(envBrokerAddr)
	}

	if os.Getenv(envMongoUri) != "" {
		mongoUri = os.Getenv(envMongoUri)
	}

	interval, err := strconv.Atoi(os.Getenv(envTickInterval))
	if !(tickInterval < 0 || err != nil) {
		tickInterval = interval
	}

	if IsUseMongo() {
		mgoClient = NewMongoClient()
	}
}

type Brokers struct {
	Members    []string `json:"members"`
	Controller string   `json:"controller"`
}

type Subscriber struct {
	GroupID string   `json:"group_id"`
	Topic   []string `json:"topics"`
}

type Subscribers struct {
	Items  []Subscriber
	filter map[string]int
}

func (s *Subscribers) Add(groupID, topic string) {
	if idx, ok := s.filter[groupID]; ok {
		for i := 0; i < len(s.Items[idx].Topic); i++ {
			if s.Items[idx].Topic[i] == topic {
				return
			}
		}
		s.Items[idx].Topic = append(s.Items[idx].Topic, topic)
		return
	}

	s.filter[groupID] = len(s.Items)
	s.Items = append(s.Items, Subscriber{
		GroupID: groupID,
		Topic:   []string{topic},
	})
}

type NextOffset struct {
	Subscriber string  `json:"subscriber"`
	Offsets    []int64 `json:"partition_next_offsets"`
}

type Offset struct {
	Subscriber string `json:"subscriber"`
	Count      int64  `json:"count"`
}

type Topic struct {
	Name             string        `json:"name"`
	Partitions       []int32       `json:"partitions"`
	Subscribers      []string      `json:"subscribers"`
	AvailableOffsets []int64       `json:"available_offsets"`
	LogSize          int64         `json:"logsize"`
	NextOffsets      []*NextOffset `json:"next_offsets"`
	Offsets          []*Offset     `json:"offset"`
}

type Topics struct {
	Items  []*Topic
	filter map[string]int
}

func (t *Topics) AddItem(item *Topic) {
	t.filter[item.Name] = len(t.Items)
	t.Items = append(t.Items, item)
}

func (t *Topics) AddSubscriber(name, subscriber string) {
	if idx, ok := t.filter[name]; ok {
		for i := 0; i < len(t.Items[idx].Subscribers); i++ {
			if t.Items[idx].Subscribers[i] == subscriber {
				return
			}
		}
		t.Items[idx].Subscribers = append(t.Items[idx].Subscribers, subscriber)
	}
}

func (t *Topics) AddNextOffsets(idx int, sub string, offset int64) {
	for i := 0; i < len(t.Items[idx].NextOffsets); i++ {
		if t.Items[idx].NextOffsets[i].Subscriber == sub {
			t.Items[idx].NextOffsets[i].Offsets = append(t.Items[idx].NextOffsets[i].Offsets, offset)
			return
		}
	}

	t.Items[idx].NextOffsets = append(
		t.Items[idx].NextOffsets,
		&NextOffset{Subscriber: sub, Offsets: []int64{offset}},
	)
}

type Metrics struct {
	Timestamp int64
	Subscribers
	Topics
	Brokers
}

func NewMetrics() *Metrics {
	return &Metrics{
		Timestamp:   time.Now().Unix(),
		Subscribers: Subscribers{filter: make(map[string]int)},
		Topics:      Topics{filter: make(map[string]int)},
	}
}

var currentMetrics *Metrics

type KafkaMonitor struct {
	kafkaCfg    *sarama.Config
	kafkaClient sarama.Client

	metrics *Metrics

	groups  map[string][]string
	brokers map[string]*sarama.Broker
}

func NewKafkaMonitor() *KafkaMonitor {
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_2_0_0
	kafkaClient, err := sarama.NewClient([]string{brokerAddr}, cfg)

	if err != nil {
		logrus.Fatalf("new kafka client error: %v", err)
	}

	bs := make(map[string]*sarama.Broker)
	for _, broker := range kafkaClient.Brokers() {
		bs[broker.Addr()] = broker
	}

	return &KafkaMonitor{
		kafkaClient: kafkaClient,
		metrics:     NewMetrics(),
		brokers:     bs,
	}
}

// reconnectBroker
func (m *KafkaMonitor) reconnectBroker(broker *sarama.Broker) error {
	if ok, _ := broker.Connected(); !ok {
		if err := broker.Open(m.kafkaClient.Config()); err != nil {
			logrus.Warnf("broker %s: connect error %v", broker.Addr(), err)
			return err
		}
	}
	return nil
}

// Refresh
func (m *KafkaMonitor) Refresh() {
	brokers := make([]string, 0)
	for k := range m.brokers {
		brokers = append(brokers, k)
	}

	m.metrics = NewMetrics()
	m.metrics.Brokers.Members = brokers

	controller, err := m.kafkaClient.Controller()
	if err != nil {
		logrus.Fatalf("could not find controller: %v", err)
	}
	m.metrics.Brokers.Controller = controller.Addr()

	topics, err := m.kafkaClient.Topics()
	if err != nil {
		logrus.Warnf("could not find topics: %v", err)
		return
	}

	for i := 0; i < len(topics); i++ {
		if topics[i] == "__consumer_offsets" {
			continue
		}

		partitions, err := m.kafkaClient.Partitions(topics[i])
		if err != nil {
			logrus.Warnf("could not find partition: %v", err)
			continue
		}

		m.metrics.Topics.AddItem(&Topic{
			Name:        topics[i],
			Partitions:  partitions,
			Subscribers: []string{},
			NextOffsets: []*NextOffset{},
			Offsets:     []*Offset{},
		})
	}

	m.refreshConsumerGroups()
}

// refreshConsumerGroups
func (m *KafkaMonitor) refreshConsumerGroups() {
	gs := make(map[string][]string)

	for _, broker := range m.brokers {
		if err := m.reconnectBroker(broker); err != nil {
			continue
		}

		resp, err := broker.ListGroups(&sarama.ListGroupsRequest{})
		if err != nil {
			logrus.Warnf("listGroups error : %v", err)
			continue
		}

		groups := make([]string, 0)
		// groupId, protocolType -> resp.Groups
		for groupId := range resp.Groups {
			groups = append(groups, groupId)
		}
		gs[broker.Addr()] = groups
	}

	m.groups = gs
	m.refreshAvailableOffsets()
}

// refreshAvailableOffsets
// AvailableOffset 表示一个主题的消息总量
func (m *KafkaMonitor) refreshAvailableOffsets() {
	for i := 0; i < len(m.metrics.Topics.Items); i++ {
		topic := m.metrics.Topics.Items[i]
		for j := 0; j < len(topic.Partitions); j++ {
			offset, _ := m.kafkaClient.GetOffset(topic.Name, topic.Partitions[j], -1)
			topic.AvailableOffsets = append(topic.AvailableOffsets, offset)
		}
	}

	m.refreshSubscriber()
}

// refreshSubscriber
func (m *KafkaMonitor) refreshSubscriber() {
	for _, broker := range m.brokers {
		if err := m.reconnectBroker(broker); err != nil {
			continue
		}

		// 获取指定 broker 对应的 consumer groups 的详细信息
		resp, err := broker.DescribeGroups(
			&sarama.DescribeGroupsRequest{Groups: m.groups[broker.Addr()]},
		)

		if err != nil {
			logrus.Warnf("describe groups error: %v", err)
			continue
		}

		// broker: groups -> 1 : N
		for i := 0; i < len(resp.Groups); i++ {
			// group: members -> 1 : N
			for _, member := range resp.Groups[i].Members {
				metadata, err := member.GetMemberMetadata()
				if err != nil {
					logrus.Warnf("member metadata error: %v", err)
					continue
				}
				// member: topics -> 1 : N
				for _, topic := range metadata.Topics {
					m.metrics.Topics.AddSubscriber(topic, resp.Groups[i].GroupId)
					m.metrics.Subscribers.Add(resp.Groups[i].GroupId, topic)
				}
			}
		}
	}

	m.refreshNextOffsets()
}

// refreshNextOffsets
// NextOffsets 表示 topic 下个即将被消费的消息的 offset
func (m *KafkaMonitor) refreshNextOffsets() {
	for i := 0; i < len(m.metrics.Topics.Items); i++ {
		topic := m.metrics.Topics.Items[i]

		// topic: subscribers -> 1 : N
		for j := 0; j < len(topic.Subscribers); j++ {
			sub := topic.Subscribers[j]
			manager, err := sarama.NewOffsetManagerFromClient(sub, m.kafkaClient)
			if err != nil {
				logrus.Warnf("manager client error: %v", err)
				continue
			}

			// topic: partitions -> 1 : N
			for k := 0; k < len(topic.Partitions); k++ {
				mp, err := manager.ManagePartition(topic.Name, topic.Partitions[k])
				if err != nil {
					logrus.Warnf("manager partition error: %v", err)
					continue
				}

				offset, _ := mp.NextOffset()
				m.metrics.Topics.AddNextOffsets(i, sub, offset)
			}
		}
	}

	m.summary()
}

// summary
func (m *KafkaMonitor) summary() {
	for i := 0; i < len(m.metrics.Topics.Items); i++ {
		topic := m.metrics.Topics.Items[i]

		// topic Available offset -> LogSize
		sumAvailable := int64(0)
		for j := 0; j < len(topic.AvailableOffsets); j++ {
			sumAvailable += topic.AvailableOffsets[j]
		}
		m.metrics.Topics.Items[i].LogSize = sumAvailable

		// topic Next offset -> Offset
		for _, item := range topic.NextOffsets {
			sumNext := int64(0)

			for j := 0; j < len(item.Offsets); j++ {
				if item.Offsets[j] != -1 {
					sumNext += item.Offsets[j]
				}
			}

			m.metrics.Topics.Items[i].Offsets = append(
				m.metrics.Topics.Items[i].Offsets,
				&Offset{Subscriber: item.Subscriber, Count: sumNext},
			)
		}
	}

	currentMetrics = m.metrics
	m.saveRecords()
}

func (m *KafkaMonitor) saveRecords() {
	if IsUseMongo() {
		checkErr(mgoClient.SaveTopics(currentMetrics.Topics.Items, m.metrics.Timestamp))
		checkErr(mgoClient.SaveSubscriber(currentMetrics.Subscribers.Items, m.metrics.Timestamp))
		checkErr(mgoClient.SaveBrokers(currentMetrics.Brokers, m.metrics.Timestamp))
	}
}

func checkErr(err error) {
	if err != nil {
		logrus.Warnf("save records err: %v", err)
	}
}

func main() {
	monitor := NewKafkaMonitor()
	monitor.Refresh()

	handleMetrics := func(w http.ResponseWriter, r *http.Request) {
		m := struct {
			Timestamp   int64        `json:"timestamp"`
			Topics      []*Topic     `json:"topics"`
			Subscribers []Subscriber `json:"subscribers"`
			Brokers     Brokers      `json:"brokers"`
		}{
			Timestamp:   currentMetrics.Timestamp,
			Topics:      currentMetrics.Topics.Items,
			Subscribers: currentMetrics.Subscribers.Items,
			Brokers:     currentMetrics.Brokers,
		}
		b, _ := json.Marshal(m)
		_, _ = fmt.Fprint(w, string(b))
	}

	http.HandleFunc("/metrics", handleMetrics)

	go func() {
		logrus.Fatal(http.ListenAndServe(":3300", nil))
	}()

	for range time.Tick(time.Duration(tickInterval) * time.Second) {
		logrus.Info("ticking.....")
		if IsUseMongo() {
			mgoClient.PingMongo()
		}
		monitor.Refresh()
	}
}
