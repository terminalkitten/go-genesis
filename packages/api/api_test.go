// Copyright 2016 The go-daylight Authors
// This file is part of the go-daylight library.
//
// The go-daylight library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-daylight library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-daylight library. If not, see <http://www.gnu.org/licenses/>.

package api

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/GenesisKernel/go-genesis/packages/consts"
	"github.com/GenesisKernel/go-genesis/packages/converter"
	"github.com/GenesisKernel/go-genesis/packages/crypto"
)

const apiAddress = "http://localhost:7079"

var (
	gAuth             string
	gAddress          string
	gPrivate, gPublic string
	gMobile           bool
)

type global struct {
	url   string
	value string
}

// PrivateToPublicHex returns the hex public key for the specified hex private key.
func PrivateToPublicHex(hexkey string) (string, error) {
	key, err := hex.DecodeString(hexkey)
	if err != nil {
		return ``, fmt.Errorf("Decode hex error")
	}
	pubKey, err := crypto.PrivateToPublic(key)
	if err != nil {
		return ``, err
	}
	return hex.EncodeToString(pubKey), nil
}

func sendRawRequest(reqType, url, contentType string, body io.Reader) ([]byte, error) {
	client := &http.Client{}
	req, err := http.NewRequest(reqType, apiAddress+consts.ApiPath+url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)

	if len(gAuth) > 0 {
		req.Header.Set("Authorization", jwtPrefix+gAuth)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(`%d %s`, resp.StatusCode, strings.TrimSpace(string(data)))
	}

	return data, nil
}

func sendRawForm(reqType, url string, form *url.Values) ([]byte, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	return sendRawRequest(reqType, url, "application/x-www-form-urlencoded", body)
}

func sendRequest(reqType, url string, form *url.Values, v interface{}) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}

	data, err := sendRawRequest(reqType, url, "application/x-www-form-urlencoded", body)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, v)
}

func sendGet(url string, form *url.Values, v interface{}) error {
	return sendRequest("GET", url, form, v)
}

func sendPost(url string, form *url.Values, v interface{}) error {
	return sendRequest("POST", url, form, v)
}

func keyLogin(state int64) (err error) {
	var (
		key, sign []byte
	)

	key, err = ioutil.ReadFile(`key`)
	if err != nil {
		return
	}
	if len(key) > 64 {
		key = key[:64]
	}
	var ret uidResult
	err = sendGet(`getuid`, nil, &ret)
	if err != nil {
		return
	}
	gAuth = ret.Token
	if len(ret.UID) == 0 {
		return fmt.Errorf(`getuid has returned empty uid`)
	}

	var pub string

	sign, err = crypto.Sign(string(key), nonceSalt+ret.UID)
	if err != nil {
		return
	}
	pub, err = PrivateToPublicHex(string(key))
	if err != nil {
		return
	}
	form := url.Values{"pubkey": {pub}, "signature": {hex.EncodeToString(sign)},
		`ecosystem`: {converter.Int64ToStr(state)}}
	if gMobile {
		form[`mobile`] = []string{`true`}
	}
	var logret loginResult
	err = sendPost(`login`, &form, &logret)
	if err != nil {
		return
	}
	gAddress = logret.Address
	gPrivate = string(key)
	gPublic, err = PrivateToPublicHex(gPrivate)
	gAuth = logret.Token
	if err != nil {
		return
	}
	return
}

func getSign(forSign string) (string, error) {
	sign, err := crypto.Sign(gPrivate, forSign)
	if err != nil {
		return ``, err
	}
	return hex.EncodeToString(sign), nil
}

func appendSign(ret map[string]interface{}, form *url.Values) error {
	forsign := ret[`forsign`].(string)
	if ret[`signs`] != nil {
		for _, item := range ret[`signs`].([]interface{}) {
			v := item.(map[string]interface{})
			vsign, err := getSign(v[`forsign`].(string))
			if err != nil {
				return err
			}
			(*form)[v[`field`].(string)] = []string{vsign}
			forsign += `,` + vsign
		}
	}
	sign, err := getSign(forsign)
	if err != nil {
		return err
	}
	(*form)[`time`] = []string{ret[`time`].(string)}
	(*form)[`signature`] = []string{sign}
	return nil
}

func waitTx(hash string) (int64, error) {
	for i := 0; i < 15; i++ {
		var ret txstatusResult
		err := sendGet(`txstatus/`+hash, nil, &ret)
		if err != nil {
			return 0, err
		}
		if len(ret.BlockID) > 0 {
			return converter.StrToInt64(ret.BlockID), fmt.Errorf(ret.Result)
		}
		if ret.Message != nil {
			errtext, err := json.Marshal(ret.Message)
			if err != nil {
				return 0, err
			}
			return 0, errors.New(string(errtext))
		}
		time.Sleep(time.Second)
	}
	return 0, fmt.Errorf(`TxStatus timeout`)
}

func randName(prefix string) string {
	return fmt.Sprintf(`%s%d`, prefix, time.Now().Unix())
}

func postTxResult(txname string, form *url.Values) (id int64, msg string, err error) {
	tx := newTxForm()

	params := make(map[string]string)
	for k, v := range *form {
		params[k] = v[0]
	}
	tx.Add(txname, params, nil)

	if len(form.Get("nowait")) > 0 {
		tx.NoWait()
	}

	if len(form.Get("vde")) > 0 {
		tx.VDEMode()
	}

	var txRes []txResult
	txRes, err = tx.Send()
	if err != nil {
		return
	}

	for _, v := range txRes {
		id = converter.StrToInt64(v.BlockID)
		msg = v.Result
		return
	}

	return
}

func RawToString(input json.RawMessage) string {
	out := strings.Trim(string(input), `"`)
	return strings.Replace(out, `\"`, `"`, -1)
}

func postTx(txname string, form *url.Values) error {
	_, _, err := postTxResult(txname, form)
	return err
}

func cutErr(err error) string {
	out := err.Error()
	if off := strings.IndexByte(out, '('); off != -1 {
		out = out[:off]
	}
	return strings.TrimSpace(out)
}

func TestGetAvatar(t *testing.T) {

	err := keyLogin(1)
	assert.NoError(t, err)

	url := `http://localhost:7079` + consts.ApiPath + "avatar/-1744264011260937456"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	assert.NoError(t, err)

	if len(gAuth) > 0 {
		req.Header.Set("Authorization", jwtPrefix+gAuth)
	}

	cli := http.DefaultClient
	resp, err := cli.Do(req)
	assert.NoError(t, err)

	defer resp.Body.Close()
	mime := resp.Header.Get("Content-Type")
	expectedMime := "image/png"
	assert.Equal(t, expectedMime, mime, "content type must be a '%s' but returns '%s'", expectedMime, mime)
}

func postTxMultipart(txname string, params map[string]string, files map[string][]byte) (id int64, msg string, err error) {
	tx := newTxForm()
	tx.Add(txname, params, files)

	var txRes []txResult
	if txRes, err = tx.Send(); err != nil {
		return
	}

	if len(txRes) > 0 {
		id = converter.StrToInt64(txRes[0].BlockID)
		msg = txRes[0].Result
	}
	return
}
