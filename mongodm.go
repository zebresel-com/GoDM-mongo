/*
This package is an object document mapper for mongodb which uses the mgo adapter.

First step is to create a model, for example:

	type User struct {
		mongodm.DocumentBase `json:",inline" bson:",inline"`

		FirstName string       `json:"firstname" bson:"firstname"`
		LastName  string       `json:"lastname"	 bson:"lastname"`
		UserName  string       `json:"username"	 bson:"username"`
		Messages  interface{}  `json:"messages"	 bson:"messages" 	model:"Message" relation:"1n" autosave:"true"`
	}

It is important that each schema embeds the IDocumentBase type (mongodm.DocumentBase) and make sure that it is tagged as 'inline' for json and bson.
This base type also includes the default values id, createdAt, updatedAt and deleted. Those values are set automatically from the ODM.
The given example also uses a relation (User has Messages). Relations must always be from type interface{} for storing bson.ObjectId OR a completely
populated object. And of course we also need the related model for each stored message:

	type Message struct {
		mongodm.DocumentBase `json:",inline" bson:",inline"`

		Sender 	  string       `json:"sender" 	 bson:"sender"`
		Receiver  string       `json:"receiver"	 bson:"receiver"`
		Text  	  string       `json:"text"	 bson:"text"`
	}

Note that when you are using relations, each model will be stored in his own collection. So the values are not embedded and instead stored as object ID
or array of object ID's.

To configure a relation the ODM understands three more tags:

	model:"Message"

		This must be the struct type you want to relate to.

		Default: none, must be set

	relation:"1n"

		It is important that you specify the relation type one-to-one or one-to-many because the ODM must decide whether it accepts an array or object.

		Possible: "1n", "11"
		Default: "11"

	autosave:"true"

		If you manipulate values of the message relation in this example and then call 'Save()' on the user instance, this flag decides if this is possible or not.
		When autosave is activated, all relations will also be saved recursively. Otherwise you have to call 'Save()' manually for each relation.

		Possible: "true", "false"
		Default: "false"

But it is not necessary to always create relations - you also can use embedded types:

	type Customer struct {
		mongodm.DocumentBase `json:",inline" bson:",inline"`

		FirstName string       `json:"firstname" bson:"firstname"`
		LastName  string       `json:"lastname"	 bson:"lastname"`
		Address   *Address     `json:"address"	 bson:"address"`
	}

	type Address struct {

		City 	string       `json:"city" 	 bson:"city"`
		Street  string       `json:"street"	 bson:"street"`
		ZipCode	int16	     `json:"zip"	 bson:"zip"`
	}

Persisting a customer instance to the database would result in embedding an complete address object. You can embed all supported types.

Now that you got some models it is important to create a connection to the database and to register these models for the ODM.

*/
package mongodm

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

const REL_11 string = "11" // one-to-one relation
const REL_1N string = "1n" // one-to-many relation

var locals map[string]string

type (

	//Simple config object which has to be passed/set to create a new connection
	Config struct {
		DatabaseHost string
		DatabaseName string
		Locals       map[string]string
	}

	//The "Database" object which stores all connections
	Connection struct {
		Config        *Config
		session       *mgo.Session
		database      *mgo.Database
		modelRegistry map[string]*Model
		typeRegistry  map[string]reflect.Type
	}

	//Interface which each collection document (model) hast to implement
	IDocumentBase interface {
		GetId() bson.ObjectId
		SetId(bson.ObjectId)

		SetCreatedAt(time.Time)
		GetCreatedAt() time.Time

		SetUpdatedAt(time.Time)
		GetUpdatedAt() time.Time

		SetDeleted(bool)
		IsDeleted() bool

		SetCollection(*mgo.Collection)
		SetDocument(document IDocumentBase)
		SetConnection(*Connection)

		Save() error
		Update(interface{}) (error, map[string]interface{})
		Validate(...interface{}) (bool, []error)
		DefaultValidate() (bool, []error)
	}
)

/*
Use this method to connect to a mongo db instance. The only parameter which is expected is a *mongodm.Config object.

For example:

	dbConfig := &mongodm.Config{
		DatabaseHost: "localhost",
		DatabaseName: "mongodm_sample",
	}

	connection, err := mongodm.Connect(dbConfig)

	if err != nil {
		log.E("Database connection error: %v", err)
	}

After this step you can register your created models. See: func (*Connection) Register
*/
func Connect(config *Config) (*Connection, error) {

	con := &Connection{
		Config:        config,
		session:       nil,
		database:      nil,
		modelRegistry: make(map[string]*Model),
		typeRegistry:  make(map[string]reflect.Type),
	}

	if config.Locals == nil {
		locals = DefaultLocals
	} else {
		locals = config.Locals
	}

	err := con.Open()

	return con, err
}

func (self *Connection) document(typeName string) IDocumentBase {

	typeNameLC := strings.ToLower(typeName)

	if _, ok := self.typeRegistry[typeNameLC]; ok {

		reflectType := self.typeRegistry[typeNameLC]
		document := reflect.New(reflectType).Interface().(IDocumentBase)

		self.modelRegistry[typeNameLC].New(document)

		return document
	}

	panic(fmt.Sprintf("DB: Type '%v' is not registered", typeName))
}

func L(key string, values ...interface{}) string {

	if locals != nil {

		if _, ok := locals[key]; ok {

			return fmt.Sprintf(locals[key], values...)
		}
	}

	return key
}

/*
To create actions on each collection you have to request a model instance with this method.
Make sure that you registered your collections and schemes first, otherwise it will panic.

For example:
	User := connection.Model("User")

	User.Find() ...
*/
func (self *Connection) Model(typeName string) *Model {

	typeNameLC := strings.ToLower(typeName)

	if _, ok := self.modelRegistry[typeNameLC]; ok {

		return self.modelRegistry[typeNameLC]
	}

	panic(fmt.Sprintf("DB: Type '%v' is not registered", typeName))
}

/*
It is necessary to register your created models to the ODM to work with. Within this process
the ODM creates an internal model and type registry to work fully automatically and consistent.
Make sure you already created a connection. Registration expects a pointer to an IDocumentBase
type and the collection name where the docuements should be stored in.

For example:
	connection.Register(&User{}, "users")
	connection.Register(&Message{}, "messages")
	connection.Register(&Customer{}, "customers")
*/
func (self *Connection) Register(document IDocumentBase, collectionName string) {

	if document == nil {
		panic("document can not be nil")
	}

	reflectType := reflect.TypeOf(document)
	typeName := strings.ToLower(reflectType.Elem().Name())

	//check if model was already registered
	if _, ok := self.modelRegistry[typeName]; !ok {

		collection := self.database.C(collectionName)
		model := &Model{collection, self}

		self.modelRegistry[typeName] = model
		self.typeRegistry[typeName] = reflectType.Elem()

		fmt.Sprintf("Registered type '%v' for collection '%v'", typeName, collectionName)

	} else {
		fmt.Sprintf("Tried to register type '%v' twice", typeName)
	}
}

//Opens a database connection manually if the config was set.
//This method gets called automatically from the Connect() method.
func (self *Connection) Open() (err error) {

	defer func() {
		if r := recover(); r != nil {

			if e, ok := r.(error); ok {
				err = e
			} else if e, ok := r.(string); ok {
				err = errors.New(e)
			} else {
				err = errors.New(fmt.Sprint(r))
			}
		}
	}()

	session, err := mgo.Dial(self.Config.DatabaseHost)

	if err != nil {
		return err
	}

	self.session = session

	self.session.SetMode(mgo.Monotonic, true)

	self.database = self.session.DB(self.Config.DatabaseName)

	return nil
}

//Closes an existing database connection
func (self *Connection) Close() {

	if self.session != nil {
		self.session.Close()
	}
}
