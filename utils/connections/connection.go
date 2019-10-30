/*******************************************************************************
 * Copyright (c) 2019 IBM Corporation and others.
 * All rights reserved. This program and the accompanying materials
 * are made available under the terms of the Eclipse Public License v2.0
 * which accompanies this distribution, and is available at
 * http://www.eclipse.org/legal/epl-v20.html
 *
 * Contributors:
 *     IBM Corporation - initial API and implementation
 *******************************************************************************/

package connections

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"

	"github.com/eclipse/codewind-installer/apiroutes"
	"github.com/eclipse/codewind-installer/utils"
	"github.com/urfave/cli"
)

// connectionsSchemaVersion must be incremented when changing the Connections Config or Connection Entry
const connectionsSchemaVersion = 1

// ConnectionConfig state and possible connections
type ConnectionConfig struct {
	SchemaVersion int          `json:"schemaversion"`
	Active        string       `json:"active"`
	Connections   []Connection `json:"connections"`
}

// Connection entry
type Connection struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	URL      string `json:"url"`
	AuthURL  string `json:"auth"`
	Realm    string `json:"realm"`
	ClientID string `json:"clientid"`
}

// InitConfigFileIfRequired : Check the config file exist, if it does not then create a new default configuration
func InitConfigFileIfRequired() *ConError {
	_, err := os.Stat(getConnectionConfigFilename())
	if os.IsNotExist(err) {
		os.MkdirAll(getConnectionConfigDir(), 0777)
		return ResetConnectionsFile()
	}
	return applySchemaUpdates()
}

// ResetConnectionsFile : Creates a new / overwrites connection config file with a default single local Codewind connection
func ResetConnectionsFile() *ConError {
	// create the default local connection
	initialConfig := ConnectionConfig{
		Active:        "local",
		SchemaVersion: connectionsSchemaVersion,
		Connections: []Connection{
			Connection{
				ID:       "local",
				Label:    "Codewind local connection",
				URL:      "",
				AuthURL:  "",
				Realm:    "",
				ClientID: "",
			},
		},
	}
	body, err := json.MarshalIndent(initialConfig, "", "\t")
	if err != nil {
		return &ConError{errOpFileParse, err, err.Error()}
	}

	err = ioutil.WriteFile(getConnectionConfigFilename(), body, 0644)
	if err != nil {
		return &ConError{errOpFileWrite, err, err.Error()}
	}
	return nil
}

// FindTargetConnection : Returns the single active connection
func FindTargetConnection() (*Connection, *ConError) {
	data, conErr := loadConnectionsConfigFile()
	if conErr != nil {
		return nil, conErr
	}

	activeID := data.Active
	for i := 0; i < len(data.Connections); i++ {
		if strings.EqualFold(activeID, data.Connections[i].ID) {
			targetConnection := data.Connections[i]
			targetConnection.URL = strings.TrimSuffix(targetConnection.URL, "/")
			targetConnection.AuthURL = strings.TrimSuffix(targetConnection.AuthURL, "/")
			return &targetConnection, nil
		}
	}

	err := errors.New(errTargetNotFound)
	return nil, &ConError{errOpNotFound, err, err.Error()}
}

// GetConnectionByID : retrieve a single connection with matching ID
func GetConnectionByID(conID string) (*Connection, *ConError) {
	connectionList, conErr := GetAllConnections()
	if conErr != nil {
		return nil, conErr
	}
	for _, connection := range connectionList {
		if strings.ToUpper(connection.ID) == strings.ToUpper(conID) {
			return &connection, nil
		}
	}
	err := errors.New("Connection " + strings.ToUpper(conID) + " not found")
	return nil, &ConError{errOpNotFound, err, err.Error()}
}

// GetConnectionsConfig : Retrieves and returns the entire Connection configuration contents
func GetConnectionsConfig() (*ConnectionConfig, *ConError) {
	data, conErr := loadConnectionsConfigFile()
	if conErr != nil {
		return nil, conErr
	}
	return data, nil
}

// SetTargetConnection : If the connection is unknown return an error
func SetTargetConnection(c *cli.Context) *ConError {
	newTargetName := c.String("conid")
	data, conErr := loadConnectionsConfigFile()
	if conErr != nil {
		return conErr
	}
	foundID := ""
	for i := 0; i < len(data.Connections); i++ {
		if strings.EqualFold(newTargetName, data.Connections[i].ID) {
			foundID = data.Connections[i].ID
			break
		}
	}
	if foundID == "" {
		err := errors.New(errTargetNotFound)
		return &ConError{errOpNotFound, err, err.Error()}
	}
	data.Active = foundID
	body, err := json.MarshalIndent(data, "", "\t")
	if err != nil {
		return &ConError{errOpFileParse, err, err.Error()}
	}
	err = ioutil.WriteFile(getConnectionConfigFilename(), body, 0644)
	if err != nil {
		return &ConError{errOpFileWrite, err, err.Error()}
	}
	return nil
}

// AddConnectionToList : validates then adds a new connection to the connection config
func AddConnectionToList(httpClient utils.HTTPClient, c *cli.Context) (*Connection, *ConError) {
	connectionID := strings.ToUpper(strconv.FormatInt(utils.CreateTimestamp(), 36))
	label := strings.TrimSpace(c.String("label"))
	url := strings.TrimSpace(c.String("url"))
	if url != "" && len(strings.TrimSpace(url)) > 0 {
		url = strings.TrimSuffix(url, "/")
	}
	data, conErr := loadConnectionsConfigFile()
	if conErr != nil {
		return nil, conErr
	}

	// check the url and label are not already in use
	for i := 0; i < len(data.Connections); i++ {
		if strings.EqualFold(label, data.Connections[i].Label) || strings.EqualFold(url, data.Connections[i].URL) {
			conErr := errors.New("Connection ID: " + connectionID + " already exists. To update, first remove and then re-add")
			return nil, &ConError{errOpConflict, conErr, conErr.Error()}
		}
	}

	gatekeeperEnv, err := apiroutes.GetGatekeeperEnvironment(httpClient, url)
	if err != nil {
		return nil, &ConError{errOpGetEnv, err, err.Error()}
	}

	// create the new connection
	newConnection := Connection{
		ID:       connectionID,
		Label:    label,
		URL:      url,
		AuthURL:  gatekeeperEnv.AuthURL,
		Realm:    gatekeeperEnv.Realm,
		ClientID: gatekeeperEnv.ClientID,
	}

	// append it to the list
	data.Connections = append(data.Connections, newConnection)
	body, err := json.MarshalIndent(data, "", "\t")
	if err != nil {
		return nil, &ConError{errOpFileParse, err, err.Error()}
	}

	err = ioutil.WriteFile(getConnectionConfigFilename(), body, 0644)
	if err != nil {
		return nil, &ConError{errOpFileWrite, err, err.Error()}
	}
	return &newConnection, nil
}

// RemoveConnectionFromList : Removes the stored entry
func RemoveConnectionFromList(c *cli.Context) *ConError {
	id := strings.ToUpper(c.String("conid"))

	if strings.EqualFold(id, "LOCAL") {
		err := errors.New("Local is a required connection and must not be removed")
		return &ConError{errOpProtected, err, err.Error()}
	}

	// check connection has been registered
	_, conErr := GetConnectionByID(id)
	if conErr != nil {
		return conErr
	}

	data, conErr := loadConnectionsConfigFile()
	if conErr != nil {
		return conErr
	}

	for i := 0; i < len(data.Connections); i++ {
		if strings.EqualFold(id, data.Connections[i].ID) {
			copy(data.Connections[i:], data.Connections[i+1:])
			data.Connections = data.Connections[:len(data.Connections)-1]
		}
	}
	data.Active = "local"
	body, err := json.MarshalIndent(data, "", "\t")
	if err != nil {
		return &ConError{errOpFileParse, err, err.Error()}
	}

	err = ioutil.WriteFile(getConnectionConfigFilename(), body, 0644)
	if err != nil {
		return &ConError{errOpFileWrite, err, err.Error()}
	}
	return nil
}

// GetTargetConnection : Retrieve the connection details for the current target connection
func GetTargetConnection() (*Connection, *ConError) {
	targetConnection, err := FindTargetConnection()
	if err != nil {
		return nil, err
	}
	if targetConnection != nil {
		return targetConnection, nil
	}
	conErr := errors.New("Unable to find a matching target - set one now using the target command")
	return nil, &ConError{errOpNotFound, conErr, conErr.Error()}
}

// GetAllConnections : Retrieve all saved connections
func GetAllConnections() ([]Connection, *ConError) {
	ConnectionConfig, conErr := GetConnectionsConfig()
	if conErr != nil {
		return nil, conErr
	}
	if ConnectionConfig != nil && ConnectionConfig.Connections != nil && len(ConnectionConfig.Connections) > 0 {
		return ConnectionConfig.Connections, nil
	}
	err := errors.New("No Connections found")
	return nil, &ConError{errOpNotFound, err, err.Error()}
}

// loadConnectionsConfigFile : Load the connections configuration file from disk
// and returns the contents of the file or an error
func loadConnectionsConfigFile() (*ConnectionConfig, *ConError) {
	file, err := ioutil.ReadFile(getConnectionConfigFilename())
	if err != nil {
		return nil, &ConError{errOpFileLoad, err, err.Error()}
	}
	data := ConnectionConfig{}
	err = json.Unmarshal([]byte(file), &data)
	if err != nil {
		return nil, &ConError{errOpFileParse, err, err.Error()}
	}
	return &data, nil
}

// saveConnectionsConfigFile : Save the connections configuration file to disk
// returns an error, and error code
func saveConnectionsConfigFile(ConnectionConfig *ConnectionConfig) *ConError {
	body, err := json.MarshalIndent(ConnectionConfig, "", "\t")
	if err != nil {
		return &ConError{errOpFileParse, err, err.Error()}
	}
	conErr := ioutil.WriteFile(getConnectionConfigFilename(), body, 0644)
	if conErr != nil {
		return &ConError{errOpFileWrite, conErr, conErr.Error()}
	}
	return nil
}

// getConnectionConfigDir : get directory path to the connections file
func getConnectionConfigDir() string {
	const GOOS string = runtime.GOOS
	homeDir := ""
	if GOOS == "windows" {
		homeDir = os.Getenv("USERPROFILE")
	} else {
		homeDir = os.Getenv("HOME")
	}
	return path.Join(homeDir, ".codewind", "config")
}

// getConnectionConfigFilename  : get full file path of connections file
func getConnectionConfigFilename() string {
	return path.Join(getConnectionConfigDir(), "connections.json")
}

func loadRawConnectionsFile() ([]byte, *ConError) {
	file, err := ioutil.ReadFile(getConnectionConfigFilename())
	if err != nil {
		return nil, &ConError{errOpFileLoad, err, err.Error()}
	}
	return file, nil
}

// applySchemaUpdates : update any existing entries to use the new schema design
func applySchemaUpdates() *ConError {

	loadedFile, conErr := loadConnectionsConfigFile()
	if conErr != nil {
		return conErr
	}
	savedSchemaVersion := loadedFile.SchemaVersion

	// upgrade the schema if needed
	if savedSchemaVersion < connectionsSchemaVersion {
		file, conErr := loadRawConnectionsFile()
		if conErr != nil {
			return conErr
		}

		// apply schama updates from version 0 to version 1
		if savedSchemaVersion == 0 {

			// current config file
			ConnectionConfig := ConnectionConfigV0{}

			// create new config structure
			newConnectionConfig := ConnectionConfigV1{}

			err := json.Unmarshal([]byte(file), &ConnectionConfig)
			if err != nil {
				return &ConError{errOpFileParse, err, err.Error()}
			}

			newConnectionConfig.Active = ConnectionConfig.Active
			newConnectionConfig.SchemaVersion = 1

			// copy connections from old to new config
			originalConnectionsV0 := ConnectionConfig.Connections
			for i := 0; i < len(originalConnectionsV0); i++ {
				originalConnection := originalConnectionsV0[i]
				connectionJSON, _ := json.Marshal(originalConnection)
				var upgradedConnection ConnectionV1
				json.Unmarshal(connectionJSON, &upgradedConnection)

				// rename 'name' field to 'id'
				upgradedConnection.ID = originalConnection.Name
				newConnectionConfig.Connections = append(newConnectionConfig.Connections, upgradedConnection)
			}

			// schema has been updated
			body, err := json.MarshalIndent(newConnectionConfig, "", "\t")
			if err != nil {
				return &ConError{errOpFileParse, err, err.Error()}
			}
			err = ioutil.WriteFile(getConnectionConfigFilename(), body, 0644)
			if err != nil {
				return &ConError{errOpFileWrite, err, err.Error()}
			}
		}
	}
	return nil
}