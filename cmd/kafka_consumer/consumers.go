package main

import (
	"encoding/json"
	"fmt"
	"github.com/Shopify/sarama"
	"github.com/gocql/gocql"
	"github.com/ndjordjevic/go-sb/internal/kafka_common"
	"gopkg.in/alecthomas/kingpin.v2"
	"log"
	"os"
	"os/signal"
	"strconv"
)

func main() {
	kingpin.Parse()

	// connect to Cassandra the cluster
	cluster := gocql.NewCluster("127.0.0.1")
	cluster.Keyspace = "go_sb"
	session, _ := cluster.CreateSession()
	log.Println("Connected to Cassandra.")
	defer session.Close()

	// connect to Kafka
	config := sarama.NewConfig()
	config.Consumer.Return.Errors = true
	brokers := *kafka_common.BrokerList
	master, err := sarama.NewConsumer(brokers, config)
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := master.Close(); err != nil {
			panic(err)
		}
	}()
	log.Println("Waiting on new messages...")

	consumer, err := master.ConsumePartition(*kafka_common.Topic, 0, sarama.OffsetNewest)
	if err != nil {
		panic(err)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	doneCh := make(chan struct{})
	go func() {
		var instrument kafka_common.Instrument
		for {
			select {
			case err := <-consumer.Errors():
				fmt.Println(err)
			case msg := <-consumer.Messages():
				*kafka_common.MessageCountStart++
				log.Println("Received messages", string(msg.Key), string(msg.Value))
				err := json.Unmarshal(msg.Value, &instrument)
				if err != nil {
					return
				}

				date := strconv.Itoa(int(instrument.ExpirationDate.Year)) + "-" + strconv.Itoa(int(instrument.ExpirationDate.Month)) + "-" + strconv.Itoa(int(instrument.ExpirationDate.Day))
				insertSql := "INSERT INTO instruments (market, isin, currency, short_name, long_name, expiration_date, status) VALUES ('" + instrument.Market + "', '" + instrument.ISIN + "', '" + instrument.Currency + "', '" + instrument.ShortName + "', '" + instrument.LongName + "', '" + date + "', '" + instrument.Status + "')"
				if err := session.Query(insertSql).Exec(); err != nil {
					log.Fatal(err)
				} else {
					log.Println("Instrument is inserted/updated in Cassandra")
				}
			case <-signals:
				fmt.Println("Interrupt is detected")
				doneCh <- struct{}{}
			}
		}
	}()
	<-doneCh
	fmt.Println("Processed", *kafka_common.MessageCountStart, "messages")
}
