package users

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/appbaseio/arc/model/user"
	"github.com/appbaseio/arc/util"
	"golang.org/x/crypto/bcrypt"
)

type elasticsearch struct {
	indexName string
}

func initPlugin(indexName, mapping string) (*elasticsearch, error) {
	ctx := context.Background()

	es := &elasticsearch{indexName}
	defer func() {
		if es != nil {
			if err := es.postMasterUser(); err != nil {
				log.Printf("%s: %v", logTag, err)
			}
		}
	}()

	// Check if the meta index already exists
	exists, err := util.GetClient7().IndexExists(indexName).
		Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: error while checking if index already exists: %v",
			logTag, err)
	}
	if exists {
		log.Printf("%s: index named '%s' already exists, skipping...", logTag, indexName)

		// hash the passwords if not hashed already
		err := es.hashPasswords()
		if err != nil {
			return nil, err
		}

		return es, nil
	}

	// set the number_of_replicas to (nodes-1)
	nodes, err := util.GetTotalNodes()
	if err != nil {
		return nil, err
	}
	settings := fmt.Sprintf(mapping, nodes, nodes-1)
	// Meta index does not exists, create a new one
	_, err = util.GetClient7().CreateIndex(indexName).
		Body(settings).
		Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: error while creating index named %s: %v",
			logTag, indexName, err)
	}

	log.Printf("%s successfully created index named '%s'", logTag, indexName)
	return es, nil
}

func (es *elasticsearch) hashPasswords() error {
	// get all users
	rawUsers, err := es.getRawUsers(context.Background())
	if err != nil {
		return err
	}

	// unmarshal into list of users
	users := []user.User{}
	err = json.Unmarshal(rawUsers, &users)
	if err != nil {
		return err
	}

	for _, user := range users {
		// don't do anything if already hashed
		if user.PasswordHashType != "" {
			continue
		}

		// hash the password
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
		if err != nil {
			msg := fmt.Sprintf("an error occurred while hashing password: %v", user.Password)
			log.Printf("%s: %s: %v", logTag, msg, err)
		}

		// patch the user
		_, err = es.patchUser(context.Background(), user.Username, map[string]interface{}{
			"password":           string(hashedPassword),
			"password_hash_type": "bcrypt",
		})

		if err != nil {
			return err
		}

		log.Println(logTag, "hashed password for user", user.Username, "using bcrypt")
	}

	return nil
}

func (es *elasticsearch) postMasterUser() error {
	// Create a master user, if credentials are not provided, we create a default
	// master user. Arc shouldn't be initialized without a root user.
	username, password := os.Getenv("USERNAME"), os.Getenv("PASSWORD")
	if username == "" {
		username, password = "foo", "bar"
	}

	// hash the password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		msg := fmt.Sprintf("an error occurred while hashing password: %v", password)
		log.Printf("%s: %s: %v", logTag, msg, err)
	}

	admin, err := user.NewAdmin(username, string(hashedPassword))
	if err != nil {
		return fmt.Errorf("%s: error while creating a master user: %v", logTag, err)
	}

	admin.PasswordHashType = "bcrypt"

	if created, err := es.postUser(context.Background(), *admin); !created || err != nil {
		return fmt.Errorf("%s: error while creating a master user: %v", logTag, err)
	}
	return nil
}

func (es *elasticsearch) getUser(ctx context.Context, username string) (*user.User, error) {
	raw, err := es.getRawUser(ctx, username)
	if err != nil {
		return nil, err
	}

	var u user.User
	err = json.Unmarshal(raw, &u)
	if err != nil {
		return nil, err
	}

	return &u, nil
}

func (es *elasticsearch) getRawUsers(ctx context.Context) ([]byte, error) {
	switch util.GetVersion() {
	case 6:
		return es.getRawUsersEs6(ctx)
	default:
		return es.getRawUsersEs7(ctx)
	}
}

func (es *elasticsearch) getRawUser(ctx context.Context, username string) ([]byte, error) {
	switch util.GetVersion() {
	case 6:
		return es.getRawUserEs6(ctx, username)
	default:
		return es.getRawUserEs7(ctx, username)
	}
}

func (es *elasticsearch) postUser(ctx context.Context, u user.User) (bool, error) {
	_, err := util.GetClient7().Index().
		Refresh("wait_for").
		Index(es.indexName).
		Id(u.Username).
		BodyJson(u).
		Do(ctx)
	if err != nil {
		return false, err
	}

	return true, nil
}

func (es *elasticsearch) patchUser(ctx context.Context, username string, patch map[string]interface{}) ([]byte, error) {
	switch util.GetVersion() {
	case 6:
		return es.patchUserEs6(ctx, username, patch)
	default:
		return es.patchUserEs7(ctx, username, patch)
	}
}

func (es *elasticsearch) deleteUser(ctx context.Context, username string) (bool, error) {
	_, err := util.GetClient7().Delete().
		Refresh("wait_for").
		Index(es.indexName).
		Id(username).
		Do(ctx)
	if err != nil {
		return false, err
	}

	return true, nil
}
