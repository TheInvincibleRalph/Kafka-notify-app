package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/theinvincible/kafka-notify/user/models"

	"github.com/IBM/sarama"
	"github.com/gin-gonic/gin"
)

const (
	ProducerPort       = ":8080"
	KafkaServerAddress = "localhost:9092"
	KafkaTopic         = "notifications"
)

// ============== HELPER FUNCTIONS ==============
var ErrUserNotFoundInProducer = errors.New("user not found")

func findUserByID(id int, users []models.User) (models.User, error) { //searches for a user by ID in a slice of users and returns the user if found, otherwise returns an error.
	for _, user := range users {
		if user.ID == id {
			return user, nil
		}
	}
	return models.User{}, ErrUserNotFoundInProducer //models.User{} is the struct version of a default value for a return type. It instantiates an empty User struct with an empty string for string type(s) in the User struct, and 0 for integers.
}

func getIDFromRequest(formValue string, ctx *gin.Context) (int, error) {
	id, err := strconv.Atoi(ctx.PostForm(formValue))
	if err != nil {
		return 0, fmt.Errorf(
			"failed to parse ID from form value %s: %w", formValue, err)
	}
	return id, nil
}

// ============== KAFKA RELATED FUNCTIONS ==============
func sendKafkaMessage(producer sarama.SyncProducer, users []models.User, ctx *gin.Context, fromID, toID int) error { //sendKafkaMessage constructs and sends a Kafka message after validating users.
	message := ctx.PostForm("message") //ctx.PostForm("message") is used to extract the value associated with the key "message" from the form data of a POST request. The POST request in this context is the notification struct and the contents are FromID, ToID, and Message.

	// Find the user who is sending the message
	fromUser, err := findUserByID(fromID, users)
	if err != nil {
		return err
	}

	// Find the user who is receiving the message
	toUser, err := findUserByID(toID, users)
	if err != nil {
		return err
	}

	// Create a notification struct with the details
	notification := models.Notification{
		From:    fromUser, //fromUser is the user sending the message
		To:      toUser,   //toUser is the user receiving the message
		Message: message,  //message is what is being sent
	}

	// Serialize the notification struct into JSON format
	notificationJSON, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("failed to marshal notification: %w", err)
	}

	// Create a Kafka message with the topic, key, and serialized value
	msg := &sarama.ProducerMessage{
		Topic: KafkaTopic,
		Key:   sarama.StringEncoder(strconv.Itoa(toUser.ID)),
		Value: sarama.StringEncoder(notificationJSON),
	}

	_, _, err = producer.SendMessage(msg) //sends the Kafka message using the producer. The underscores _ are used to ignore the partition and offset values returned by SendMessage
	return err
}

// ----------------------------- This function handles the incoming HTTP POST request (it is the router's handler) -----------------------------
func sendMessageHandler(producer sarama.SyncProducer, users []models.User) gin.HandlerFunc { //sendMessageHandler is a Gin handler that processes incoming requests to send messages.

	return func(ctx *gin.Context) { //extracts fromID
		fromID, err := getIDFromRequest("fromID", ctx)
		if err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
			return
		}

		toID, err := getIDFromRequest("toID", ctx) ////extracts toID
		if err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
			return
		}

		err = sendKafkaMessage(producer, users, ctx, fromID, toID)
		if errors.Is(err, ErrUserNotFoundInProducer) {
			ctx.JSON(http.StatusNotFound, gin.H{"message": "User not found"})
			return
		}
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{
				"message": err.Error(),
			})
			return
		}

		ctx.JSON(http.StatusOK, gin.H{
			"message": "Notification sent successfully!",
		})
	}
}

/*
The sendMessageHandler function returns a gin.HandlerFunc, which is a type for Gin route handlers.
This allows the returned function to be used as a route handler in the Gin router.
The notification is processed upon receiving a POST request via the sendMessageHandler() function,
and an appropriate HTTP response is dispatched.
*/

func setupProducer() (sarama.SyncProducer, error) { //a function to setup a Kafka producer that sends messages synchronously (waits for acknowledgments).
	config := sarama.NewConfig() //this line creates a new Kafka configuration object using the sarama library. The sarama.NewConfig function returns a pointer to a new Config struct with default settings.
	config.Producer.Return.Successes = true
	producer, err := sarama.NewSyncProducer([]string{KafkaServerAddress}, config) //this line creates a new synchronous Kafka producer with the specified broker addresses and configuration.
	if err != nil {
		return nil, fmt.Errorf("failed to setup producer: %w", err)
	}
	return producer, nil
}

func main() {
	users := []models.User{
		{ID: 1, Name: "Emma"},
		{ID: 2, Name: "Bruno"},
		{ID: 3, Name: "Rick"},
		{ID: 4, Name: "Lena"},
	}

	producer, err := setupProducer()
	if err != nil {
		log.Fatalf("failed to initialize producer: %v", err)
	}
	defer producer.Close() //this line ensures that the producer is properly closed when the main function exits, releasing any resources it holds.

	gin.SetMode(gin.ReleaseMode) //sets Gin to release mode, which disables debug output and optimizes performance.
	router := gin.Default()      //creates a new Gin router with default middleware (like logging and recovery).
	router.POST("/send", sendMessageHandler(producer, users))

	fmt.Printf("Kafka PRODUCER 📨 started at http://localhost%s\n",
		ProducerPort)

	if err := router.Run(ProducerPort); err != nil { //starts the Gin HTTP server on the specified port and logs any error encountered
		log.Printf("failed to run the server: %v", err)
	}
}
