package main

import (
	"fmt"
	"github.com/Shopify/sarama"
	"github.com/ndjordjevic/go-sb/internal/kafka_common"
	"google.golang.org/genproto/googleapis/type/date"
	"gopkg.in/alecthomas/kingpin.v2"
)

func main() {
	instrumentToSend := kafka_common.Instrument{
		Market:    "Xetra",
		ISIN:      "BMW001",
		Currency:  "SEK",
		ShortName: "BMW",
		LongName:  "BMW Incorporation",
		LotSize:   1,
		ExpirationDate: date.Date{
			Year:  2019,
			Month: 10,
			Day:   31,
		},
		Status: "ACTIVE",
	}

	byteArray := kafka_common.ConvertToByteArray(instrumentToSend)

	fmt.Println(byteArray)
	fmt.Println(string(byteArray))

	kingpin.Parse()
	config := sarama.NewConfig()
	config.Producer.RequiredAcks = sarama.WaitForAll
	config.Producer.Retry.Max = *kafka_common.MaxRetry
	config.Producer.Return.Successes = true
	producer, err := sarama.NewSyncProducer(*kafka_common.BrokerList, config)
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := producer.Close(); err != nil {
			panic(err)
		}
	}()
	msg := &sarama.ProducerMessage{
		Topic: *kafka_common.InstrumentTopic,
		Value: sarama.ByteEncoder(byteArray),
	}
	partition, offset, err := producer.SendMessage(msg)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Message is stored in topic(%s)/partition(%d)/offset(%d)\n", *kafka_common.InstrumentTopic, partition, offset)
}
