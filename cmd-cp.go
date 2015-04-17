/*
 * Modern Copy, (C) 2014,2015 Minio, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"io"

	"github.com/cheggaaa/pb"
	"github.com/minio-io/cli"
	"github.com/minio-io/mc/pkg/console"
	"github.com/minio-io/minio/pkg/iodine"
	"github.com/minio-io/minio/pkg/utils/log"
)

type clientManager interface {
	getSourceReader(sourceURLParser *parsedURL) (reader io.ReadCloser, length int64, md5hex string, err error)
	getTargetWriter(targetURLParser *parsedURL, md5Hex string, length int64) (io.WriteCloser, error)
}

type mcClientManager struct{}

func (manager mcClientManager) getSourceReader(sourceURLParser *parsedURL) (reader io.ReadCloser, length int64, md5hex string, err error) {
	sourceClnt, err := getNewClient(sourceURLParser, globalDebugFlag)
	if err != nil {
		return nil, 0, "", iodine.New(err, map[string]string{"sourceURL": sourceURLParser.String()})
	}
	// Get a reader for the source object
	sourceBucket := sourceURLParser.bucketName
	// check if the bucket is valid
	if err := sourceClnt.StatBucket(sourceBucket); err != nil {
		return nil, 0, "", iodine.New(err, map[string]string{"sourceURL": sourceURLParser.String()})
	}
	sourceObject := sourceURLParser.objectName
	return sourceClnt.Get(sourceBucket, sourceObject)
}

func (manager mcClientManager) getTargetWriter(targetURLParser *parsedURL, md5Hex string, length int64) (io.WriteCloser, error) {
	targetClnt, err := getNewClient(targetURLParser, globalDebugFlag)
	if err != nil {
		return nil, iodine.New(err, map[string]string{"failedURL": targetURLParser.String()})
	}
	targetBucket := targetURLParser.bucketName
	// check if bucket is valid
	if err := targetClnt.StatBucket(targetBucket); err != nil {
		return nil, iodine.New(err, map[string]string{"failedURL": targetURLParser.String()})
	}
	targetObject := targetURLParser.objectName
	return targetClnt.Put(targetBucket, targetObject, md5Hex, length)
}

func getTargetWriters(manager clientManager, targetURLParsers []*parsedURL, md5Hex string, length int64) ([]io.WriteCloser, error) {
	var targetWriters []io.WriteCloser
	for _, targetURLParser := range targetURLParsers {
		writer, err := manager.getTargetWriter(targetURLParser, md5Hex, length)
		if err != nil {
			// close all writers
			for _, targetWriter := range targetWriters {
				targetWriter.Close()
			}
			return nil, iodine.New(err, map[string]string{"failedURL": targetURLParser.String()})
		}
		targetWriters = append(targetWriters, writer)
	}
	return targetWriters, nil
}

// doCopyCmd copies objects into and from a bucket or between buckets
func runCopyCmd(ctx *cli.Context) {
	if len(ctx.Args()) < 2 {
		cli.ShowCommandHelpAndExit(ctx, "cp", 1) // last argument is exit code
	}

	// Convert arguments to URLs: expand alias, fix format...
	urls, err := parseURLs(ctx)
	if err != nil {
		log.Debug.Println(iodine.New(err, nil))
		console.Fatalln("mc: Unable to parse URL")
	}
	sourceURL := urls[0]   // First arg is source
	targetURLs := urls[1:] // 1 or more targets

	// perform copy
	if ctx.Bool("recursive") {
		doCopyCmdRecursive(ctx)
	} else {
		humanReadableError, err := doCopyCmd(mcClientManager{}, sourceURL, targetURLs)
		err = iodine.New(err, nil)
		if err != nil {
			if humanReadableError == "" {
				humanReadableError = "No error message present, please rerun with --debug and report a bug."
			}
			log.Debug.Println(err)
			console.Errorln("mc: " + humanReadableError)
		}
	}
}

func doCopyCmd(manager clientManager, sourceURL *parsedURL, targetURLs []*parsedURL) (string, error) {
	reader, length, hexMd5, err := manager.getSourceReader(sourceURL)
	if err != nil {
		return "Unable to read from source", iodine.New(err, nil)
		//		console.Errorln("mc: Unable to read source")
	}
	defer reader.Close()

	writeClosers, err := getTargetWriters(manager, targetURLs, hexMd5, length)
	if err != nil {
		return "Unable to write to target", iodine.New(err, nil)
	}

	var writers []io.Writer
	for _, writer := range writeClosers {
		writers = append(writers, writer)
	}

	// set up progress bar
	var bar *pb.ProgressBar
	if !globalQuietFlag {
		bar = startBar(length)
		bar.Start()
		writers = append(writers, bar)
	}

	// write progress bar
	multiWriter := io.MultiWriter(writers...)

	// copy data to writers
	_, err = io.CopyN(multiWriter, reader, length)

	// close writers
	for _, writer := range writeClosers {
		err := writer.Close()
		if err != nil {
			err = iodine.New(err, nil)
		}
	}
	if err != nil {
		return "Unable to close all connections, write may of failed.", iodine.New(err, nil)
	}
	return "", nil
}
