<h1 align="center">🖇 kfk</h1>
<p align="center"><em>Query kafka-cluster infomations in one place</em></p>

[kafka](http://kafka.apache.org/) 是由 Apache 软件基金会开发的一个开源流处理平台，由 Scala 和 Java 编写。查询 kafka 的信息并不是特别方便，它没有类似其他数据库那样的 shell 环境。

[kfk](https://github.com/chenjiandongx/kfk.git) 是一个采集 kafka `topics/subscribers/partitions/brokers` 信息的工具。


### ✨ Feature

1. 轻量（如果不需要将数据保存至数据库的话，直接构建运行即可）
2. 开箱即用

### 📑 TODO

* 将数据写入到 InfluxDB/Prometheus

### ⛏ 构建运行

0. 环境变量

    | 变量名 | 说明 | 默认值 |
    | -----  | --- | ----- |
    | BROKER_ADDR | kafka broker_uri（如果是集群环境，只需指定其中一个成员即可） | localhost:9092 |
    | MONGO_URI | mongo_uri（Mongodb 连接字符串，不指定则不使用 Mongo）| 无 |
    | TICK_INTERVAL | 查询 kafka 信息时间间隔 | 10（单位 s） |  

1. 拉取项目

    ```shell
    $ git clone https://github.com/chenjiandongx/kfk.git && cd kfk
    ```

2. 构建项目

    ```shell
    # 本地构建
    $ export BROKER_ADDR="your_kafka_addr:port"
    $ export MONGO_URI="mongodb://your_mongo_addr:27017"
    $ RUN go build  -o kfk .
    ```

    OR

    ```shell
    # 使用 Docker（推荐）
    $ docker build --tag kfk .
    $ docker run -d -p 3300:3300 --env BROKER_ADDR=broker1:9092 kfk
    ```

kfk 程序运行后，会启动一个 HTTP 服务，默认监听 `3300` 端口（Docker 运行的话则需要对外暴露该端口）

### 📝 使用示例

HTTP 路由为 `/metrics`

```shell
$ curl http://localhost:3300/metrics | jq

{
  "timestamp": 1560825753,
  "topics": [
    {
      "name": "TEST_TOPCI_1",
      "partitions": [
          0,
          1,
          2,
          3,
          4,
          5
        ],
      "subscribers": [
        {
          "next_offsets": [
              188302, 
              177512, 
              168999, 
              189982, 
              190677, 
              172268
            ],
          "offset": 1087740,
          "group_id": "fake_group_1"
        }
      ],
      "available_offsets": [
          188302, 
          177512, 
          168999, 
          189982, 
          190677, 
          172268
        ],
      "logsize": 1087740
    },
    {
      "name": "TEST_TOPCI_2",
      "partitions": [
          0, 
          1, 
          2, 
          3, 
          4, 
          5
        ],
      "subscribers": [
        {
          "next_offsets": [
              -1, 
              -1, 
              -1, 
              444, 
              -1, 
              4236
            ],
          "offset": 4680,
          "group_id": [
              "fake_group_1", 
              "fake_group_2"
            ]
        }
      ],
      "available_offsets": [
          0, 
          13914,
          0, 
          444,
          0, 
          4236
        ],
      "logsize": 18594
    },
    
  ],
  "subscribers": [
    {
      "group_id": "fake_group_1",
      "topics": [
          "TEST_TOPCI_1", 
          "TEST_TOPCI_2"
        ]
    },
    {
      "group_id": "fake_group_2",
      "topics": [
          "TEST_TOPCI_2"
        ]
    }
  ],
  "brokers": {
    "members": [
        "broker2:9092", 
        "broker1:9092", 
        "broker3:9092"
    ],
    "controller": "broker2:9092"
  }
}
```

metrics 信息说明

| 参数 | 类型  | 说明 |
| ---- | ---- | --- |
| `timestamp` | int | 数据更新时间 |
| `topics` | array object | 主题列表 |
| `topics[idx].name` | string | 主题名称 |
| `topics[idx].partitions` | array int | 主题存储分区列表 |
| `topics[idx].subscrbisers` | array object | 主题订阅者列表 |
| `topics[idx].subscrbisers[idx].next_offsets` | array | 主题订阅者对应分区的下一个即将被消费的消息的 offset |
| `topics[idx].subscrbisers[idx].offset` | int | 主题订阅者已经消费的 offset |
| `topics[idx].subscrbisers[idx].group_id` | string | 主题订阅者 ID |
| `subsrcibers` | array object | 订阅者列表 |
| `subsrcibers[idx].group_id` | string | 订阅者 ID |
| `subsrcibers[idx].topics` | array string | 订阅者订阅的主题 |
| `brokers` | object | brokers 节点信息 |
| `brokers.members` | array string | brokers 成员节点 |
| `brokers.controller` | string | brokers 的 controller 节点 |

### 🗂 Database

#### 🥭 MongoDB

如果指定了 mongo_uri，则数据同时会被写入到数据库

```shell
> show dbs
# 可以看到新增了 `kfk` db
admin       0.000GB
local       0.000GB
kfk         0.000GB
> use kfk
# 切换到 kfk db
switched to db kfk
>
> show tables
# 新增了三个数据表，写入的信息跟 /metrics 查询到的信息一致
brokers
subscribers
topics
> # find everything you want
```

## 📃 License

MIT [©chenjiandongx](https://github.com/chenjiandongx)
