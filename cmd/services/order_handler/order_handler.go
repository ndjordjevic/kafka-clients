package main

import (
	"context"
	"fmt"
	"github.com/gocql/gocql"
	orderpb "github.com/ndjordjevic/go-sb/api/order"
	"github.com/ndjordjevic/go-sb/cmd/common"
	"github.com/olivere/elastic/v7"
	"google.golang.org/grpc"
	"google.golang.org/grpc/resolver"
	"log"
	"net"
	"time"
)

type server struct{}

const mapping = `
{
  "settings": {
    "number_of_shards": 1,
    "number_of_replicas": 0
  },
  "mappings": {
    "properties": {
      "uuid": {
        "type": "keyword"
      },
      "email": {
        "type": "keyword"
      },
      "instrument_key": {
        "type": "text",
        "store": true,
        "fielddata": true
      },
      "currency": {
        "type": "keyword"
      },
      "size": {
        "type": "float"
      },
      "price": {
        "type": "float"
      },
      "status": {
        "type": "keyword"
      },
      "created": {
        "type": "date"
      }
    }
  }
}
`

const (
	exampleScheme      = "example"
	exampleServiceName = "lb.example.grpc.io"
)

var addrs = []string{"order_validator:50061", "order_validator:50062"}

var session *gocql.Session

func init() {
	// connect to Cassandra cluster
	//cluster := gocql.NewCluster("127.0.0.1")
	cluster := gocql.NewCluster("host.docker.internal")
	cluster.Keyspace = "go_sb"
	session, _ = cluster.CreateSession()
	log.Println("Connected to Cassandra.")

	resolver.Register(&exampleResolverBuilder{})
}

func (*server) HandleOrder(ctx context.Context, req *orderpb.HandleOrderRequest) (*orderpb.HandleOrderResponse, error) {
	log.Println("New request to handle", req.Order)

	order := common.Order{
		Email:         req.Order.Email,
		InstrumentKey: req.Order.InstrumentKey,
		Currency:      req.Order.Currency,
		Size:          req.Order.Size,
		Price:         req.Order.Price,
	}

	resValidateOrder := callOrderValidatorService(order)

	res := &orderpb.HandleOrderResponse{}

	if resValidateOrder.Valid {
		res.Response = orderpb.HandleOrderResponse_OK

		order.Created = time.Now()
		order.UUID = gocql.TimeUUID()
		order.Status = "ACTIVE"

		writeOrderToDBAsync(order)
		writeOrderToESAsync(order)

		log.Println("Order's valid")
	} else {
		log.Println(resValidateOrder.GetErrorMessage())
		res.Response = orderpb.HandleOrderResponse_ERROR
		res.ErrorMessage = resValidateOrder.GetErrorMessage()
	}

	return res, nil
}

func writeOrderToESAsync(order common.Order) {
	go func() {
		// Starting with elastic.v5, you must pass a context to execute each service
		ctx := context.Background()

		// Obtain a client and connect to the default Elasticsearch installation
		// on 127.0.0.1:9200. Of course you can configure your client to connect
		// to other hosts and configure it in various other ways.
		//client, err := elastic.NewClient()
		client, err := elastic.NewSimpleClient(elastic.SetURL("http://host.docker.internal:9200"))
		if err != nil {
			// Handle error
			panic(err)
		}

		// Ping the Elasticsearch server to get e.g. the version number
		//info, code, err := client.Ping("http://localhost:9200").Do(ctx)
		info, code, err := client.Ping("http://host.docker.internal:9200").Do(ctx)
		if err != nil {
			// Handle error
			panic(err)
		}
		log.Printf("Elasticsearch returned with code %d and version %s\n", code, info.Version.Number)

		// Use the IndexExists service to check if a specified index exists.
		exists, err := client.IndexExists("order").Do(ctx)
		if err != nil {
			// Handle error
			panic(err)
		}

		if !exists {
			//Create a new index.
			createIndex, err := client.CreateIndex("order").Body(mapping).Do(ctx)
			if err != nil {
				// Handle error
				panic(err)
			}
			if !createIndex.Acknowledged {
				log.Println("Not Acknowledged")
			}
		}

		put1, err := client.Index().
			Index("order").
			Id(order.UUID.String()).
			BodyJson(order).
			Do(ctx)
		if err != nil {
			// Handle error
			panic(err)
		}

		fmt.Printf("Indexed orders %s to index %s\n", put1.Id, put1.Index)
	}()
}

func callOrderValidatorService(order common.Order) *orderpb.ValidateOrderResponse {
	reqValidateOrder := &orderpb.ValidateOrderRequest{
		Order: &orderpb.Order{
			Email:         order.Email,
			InstrumentKey: order.InstrumentKey,
			Currency:      order.Currency,
			Size:          order.Size,
			Price:         order.Price,
		},
	}

	// validation grpc client connection
	clientConn, err := grpc.Dial(fmt.Sprintf("%s:///%s", exampleScheme, exampleServiceName),
		//grpc.WithBalancerName("round_robin"), // comment this to turn off round_robin LB and turn on pic_first (tries to connect to first address and if its not available dials others)
		grpc.WithInsecure())

	if err != nil {
		log.Fatal(err)
	}

	defer func() {
		if err := clientConn.Close(); err != nil {
			log.Fatal(err)
		}
	}()

	// grpc service client
	validateOrderServiceClient := orderpb.NewOrderValidatorServiceClient(clientConn)
	resValidateOrder, err := validateOrderServiceClient.ValidateOrder(context.Background(), reqValidateOrder)
	if err != nil {
		log.Fatal(err)
	}

	return resValidateOrder
}

func writeOrderToDBAsync(order common.Order) {
	go func() {

		// write order to Cassandra
		if err := session.Query(`INSERT INTO orders (uuid, email, instrument_key, currency, size, price, status, created) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			order.UUID, order.Email, order.InstrumentKey, order.Currency, order.Size, order.Price, order.Status, order.Created).Exec(); err != nil {
			log.Fatal(err)
		}
	}()
}

func main() {
	// order handler grpc service server
	lis, err := net.Listen("tcp", ":50051")

	if err != nil {
		log.Fatal(err)
	}

	s := grpc.NewServer()

	orderpb.RegisterOrderHandlerServiceServer(s, &server{})

	if err := s.Serve(lis); err != nil {
		log.Fatal(err)
	}
}

// Following is an example name resolver implementation. Read the name
// resolution example to learn more about it.

type exampleResolverBuilder struct{}

func (*exampleResolverBuilder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOption) (resolver.Resolver, error) {
	r := &exampleResolver{
		target: target,
		cc:     cc,
		addrsStore: map[string][]string{
			exampleServiceName: addrs,
		},
	}
	r.start()
	return r, nil
}
func (*exampleResolverBuilder) Scheme() string { return exampleScheme }

type exampleResolver struct {
	target     resolver.Target
	cc         resolver.ClientConn
	addrsStore map[string][]string
}

func (r *exampleResolver) start() {
	addrStrs := r.addrsStore[r.target.Endpoint]
	addrs := make([]resolver.Address, len(addrStrs))
	for i, s := range addrStrs {
		addrs[i] = resolver.Address{Addr: s}
	}
	r.cc.UpdateState(resolver.State{Addresses: addrs})
}
func (*exampleResolver) ResolveNow(o resolver.ResolveNowOption) {}
func (*exampleResolver) Close()                                 {}
