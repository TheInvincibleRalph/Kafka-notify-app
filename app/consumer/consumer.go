/*
After creating the producer, the next step is to set up a consumer
that listens to the "notifications" topic and provides an endpoint
to list notifications for a specific user.
*/

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/theinvincible/kafka-notify/user/models"

	"github.com/IBM/sarama" //sarama is a Go library used for interacting with Apache Kafka. It provides the necessary tools and abstractions to produce and consume messages from Kafka topics.
	"github.com/gin-gonic/gin"
)

const (
	ConsumerGroup      = "notifications-group"
	ConsumerTopic      = "notifications"
	ConsumerPort       = ":8081"
	KafkaServerAddress = "localhost:9092"
)

// ============== HELPER FUNCTIONS ==============
var ErrNoMessagesFound = errors.New("no messages found")

func getUserIDFromRequest(ctx *gin.Context) (string, error) {
	userID := ctx.Param("userID") //this method retrieves the value of the URL parameter named userID
	if userID == "" {
		return "", ErrNoMessagesFound
	}
	return userID, nil
}

// ====== NOTIFICATION STORAGE ======

/*
The NotificationStore struct is designed to store and manage user notifications
in a concurrent-safe manner. The sync.RWMutex ensures that multiple goroutines
can safely read from and write to the data without causing race conditions or data corruption.
*/

type UserNotifications map[string][]models.Notification

type NotificationStore struct {
	data UserNotifications //this field holds the actual data of user notifications
	mu   sync.RWMutex
}

func (ns *NotificationStore) Add(userID string, notification models.Notification) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.data[userID] = append(ns.data[userID], notification)
}

func (ns *NotificationStore) Get(userID string) []models.Notification {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return ns.data[userID]
}

// ============== KAFKA RELATED FUNCTIONS ==============
type Consumer struct {
	store *NotificationStore //this store is used to keep track of notifications
}

func (*Consumer) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (*Consumer) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func initializeConsumerGroup() (sarama.ConsumerGroup, error) {
	config := sarama.NewConfig()

	consumerGroup, err := sarama.NewConsumerGroup([]string{KafkaServerAddress}, ConsumerGroup, config)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize consumer group: %w", err)
	}

	return consumerGroup, nil
}

func setupConsumerGroup(ctx context.Context, store *NotificationStore) { //this function sets up and runs a consumer group responsible for consuming messages from a kafka topic
	consumerGroup, err := initializeConsumerGroup()
	if err != nil {
		log.Printf("initialization error: %v", err)
	}
	defer consumerGroup.Close()

	consumer := &Consumer{ //creates a new Consumer with the provided notification store
		store: store,
	}

	for { //continuously consumes messages from the Kafka topic
		err = consumerGroup.Consume(ctx, []string{ConsumerTopic}, consumer)
		if err != nil {
			log.Printf("error from consumer: %v", err)
		}
		if ctx.Err() != nil { //exits the loop if the context is canceled
			return
		}
	}
}

func (consumer *Consumer) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error { //this claim represents the partitions assigned to the consumer for processing.
	for msg := range claim.Messages() { //iterates over the messages in the claim
		userID := string(msg.Key) //converts the message key (which is a byte array) to a string, representing the user ID
		var notification models.Notification
		err := json.Unmarshal(msg.Value, &notification) //unmarshals the JSON-encoded message value into the notification variable
		if err != nil {
			log.Printf("failed to unmarshal notification: %v", err)
			continue
		}
		consumer.store.Add(userID, notification) //adds the notification to the NotificationStore
		sess.MarkMessage(msg, "")                //marks the message as processed in the session
	}
	return nil
}

func handleNotifications(ctx *gin.Context, store *NotificationStore) { //this function handles HTTP requests to retrieve notifications for a user
	userID, err := getUserIDFromRequest(ctx)
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
		return
	}

	notes := store.Get(userID)
	if len(notes) == 0 {
		ctx.JSON(http.StatusOK,
			gin.H{
				"message":       "No notifications found for user",
				"notifications": []models.Notification{},
			})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"notifications": notes})
}

func main() {
	store := &NotificationStore{
		data: make(UserNotifications),
	}

	ctx, cancel := context.WithCancel(context.Background())
	go setupConsumerGroup(ctx, store)
	defer cancel()

	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()
	router.GET("/notifications/:userID", func(ctx *gin.Context) {
		handleNotifications(ctx, store)
	})

	fmt.Printf("Kafka CONSUMER (Group: %s) 👥📥 "+"started at http://localhost%s\n", ConsumerGroup, ConsumerPort)

	if err := router.Run(ConsumerPort); err != nil {
		log.Printf("failed to run the server: %v", err)
	}
}
