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
)

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

type TopicSubscriber struct {
	NextOffsets []int64 `json:"next_offsets"`
	Offset      int64   `json:"offset"`
	GroupID     string  `json:"group_id"`
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

type Topic struct {
	Name             string             `json:"name"`
	Partitions       []int32            `json:"partitions"`
	Subscribers      []*TopicSubscriber `json:"subscribers"`
	AvailableOffsets []int64            `json:"available_offsets"`
	LogSize          int64              `json:"logsize"`
}

type Topics struct {
	Items  []*Topic
	filter map[string]int
}

func (t *Topics) AddItem(item *Topic) {
	t.filter[item.Name] = len(t.Items)
	t.Items = append(t.Items, item)
}

func (t *Topics) AddSubscriber(topicName, subscriber string) {
	if idx, ok := t.filter[topicName]; ok {
		for i := 0; i < len(t.Items[idx].Subscribers); i++ {
			if t.Items[idx].Subscribers[i].GroupID == subscriber {
				return
			}
		}
		t.Items[idx].Subscribers = append(t.Items[idx].Subscribers, &TopicSubscriber{GroupID: subscriber})
	}
}

func (t *Topics) AddNextOffsets(idx int, subscriber string, offset int64) {
	for i := 0; i < len(t.Items[idx].Subscribers); i++ {
		if t.Items[idx].Subscribers[i].GroupID == subscriber {
			t.Items[idx].Subscribers[i].NextOffsets = append(t.Items[idx].Subscribers[i].NextOffsets, offset)
			return
		}
	}
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
			Subscribers: []*TopicSubscriber{},
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
			manager, err := sarama.NewOffsetManagerFromClient(topic.Subscribers[j].GroupID, m.kafkaClient)
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
				m.metrics.Topics.AddNextOffsets(i, topic.Subscribers[j].GroupID, offset)
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
		for _, item := range topic.Subscribers {
			sumNext := int64(0)

			for j := 0; j < len(item.NextOffsets); j++ {
				if item.NextOffsets[j] != -1 {
					sumNext += item.NextOffsets[j]
				}
			}
			item.Offset = sumNext
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
