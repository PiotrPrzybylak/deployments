// Copyright 2016 Mender Software AS
//
//    Licensed under the Apache License, Version 2.0 (the "License");
//    you may not use this file except in compliance with the License.
//    You may obtain a copy of the License at
//
//        http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS,
//    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//    See the License for the specific language governing permissions and
//    limitations under the License.

package controller

import (
	"io"
	"io/ioutil"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/ant0ine/go-json-rest/rest"
	"github.com/asaskevich/govalidator"
	"github.com/mendersoftware/deployments/resources/images"
	"github.com/pkg/errors"
)

// API input validation constants
const (
	DefaultDownloadLinkExpire = 60

	// AWS limitation is 1 week
	MaxLinkExpire = 60 * 7 * 24
)

var (
	ErrIDNotUUIDv4        = errors.New("ID is not UUIDv4")
	ErrInvalidExpireParam = errors.New("Invalid expire parameter")
)

type SoftwareImagesController struct {
	view  RESTView
	model ImagesModel
}

func NewSoftwareImagesController(model ImagesModel, view RESTView) *SoftwareImagesController {
	return &SoftwareImagesController{
		model: model,
		view:  view,
	}
}

func (s *SoftwareImagesController) GetImage(w rest.ResponseWriter, r *rest.Request) {

	id := r.PathParam("id")

	if !govalidator.IsUUIDv4(id) {
		s.view.RenderError(w, ErrIDNotUUIDv4, http.StatusBadRequest)
		return
	}

	image, err := s.model.GetImage(id)
	if err != nil {
		s.view.RenderError(w, err, http.StatusInternalServerError)
		return
	}

	if image == nil {
		s.view.RenderErrorNotFound(w)
		return
	}

	s.view.RenderSuccessGet(w, image)
}

func (s *SoftwareImagesController) ListImages(w rest.ResponseWriter, r *rest.Request) {

	list, err := s.model.ListImages(r.PathParams)
	if err != nil {
		s.view.RenderError(w, err, http.StatusInternalServerError)
		return
	}

	s.view.RenderSuccessGet(w, list)
}

func (s *SoftwareImagesController) DownloadLink(w rest.ResponseWriter, r *rest.Request) {

	id := r.PathParam("id")

	if !govalidator.IsUUIDv4(id) {
		s.view.RenderError(w, ErrIDNotUUIDv4, http.StatusBadRequest)
		return
	}

	expire, err := s.getLinkExpireParam(r, DefaultDownloadLinkExpire)
	if err != nil {
		s.view.RenderError(w, err, http.StatusBadRequest)
		return
	}

	link, err := s.model.DownloadLink(id, expire)
	if err != nil {
		s.view.RenderError(w, err, http.StatusInternalServerError)
		return
	}

	if link == nil {
		s.view.RenderErrorNotFound(w)
		return
	}

	s.view.RenderSuccessGet(w, link)
}

func (s *SoftwareImagesController) getLinkExpireParam(r *rest.Request, defaultValue uint64) (time.Duration, error) {

	expire := defaultValue
	expireStr := r.URL.Query().Get("expire")

	// Validate input
	if !govalidator.IsNull(expireStr) {
		if !s.validExpire(expireStr) {
			return 0, ErrInvalidExpireParam
		}

		var err error
		expire, err = strconv.ParseUint(expireStr, 10, 64)
		if err != nil {
			return 0, err
		}
	}

	return time.Duration(int64(expire)) * time.Minute, nil
}

func (s *SoftwareImagesController) validExpire(expire string) bool {

	if govalidator.IsNull(expire) {
		return false
	}

	number, err := strconv.ParseUint(expire, 10, 64)
	if err != nil {
		return false
	}

	if number > MaxLinkExpire {
		return false
	}

	return true
}

func (s *SoftwareImagesController) DeleteImage(w rest.ResponseWriter, r *rest.Request) {

	id := r.PathParam("id")

	if !govalidator.IsUUIDv4(id) {
		s.view.RenderError(w, ErrIDNotUUIDv4, http.StatusBadRequest)
		return
	}

	if err := s.model.DeleteImage(id); err != nil {
		if err == ErrImageMetaNotFound {
			s.view.RenderErrorNotFound(w)
			return
		}
		s.view.RenderError(w, err, http.StatusInternalServerError)
		return
	}

	s.view.RenderSuccessDelete(w)
}

func (s *SoftwareImagesController) EditImage(w rest.ResponseWriter, r *rest.Request) {

	id := r.PathParam("id")

	if !govalidator.IsUUIDv4(id) {
		s.view.RenderError(w, ErrIDNotUUIDv4, http.StatusBadRequest)
		return
	}

	constructor, err := s.getSoftwareImageConstructorFromBody(r)
	if err != nil {
		s.view.RenderError(w, errors.Wrap(err, "Validating request body"), http.StatusBadRequest)
		return
	}

	found, err := s.model.EditImage(id, constructor)
	if err != nil {
		s.view.RenderError(w, err, http.StatusInternalServerError)
		return
	}

	if !found {
		s.view.RenderErrorNotFound(w)
		return
	}

	s.view.RenderSuccessPut(w)
}

// Multipart Image/Meta upload handler.
// Request should be of type "multipart/form-data".
// First part should contain Metadata file. This file should be of type "application/json".
// Second part should contain Image file. This part should be of type "application/octet-strem".
func (s *SoftwareImagesController) NewImage(w rest.ResponseWriter, r *rest.Request) {

	// limits just for safety;
	const (
		// Max image size
		DefaultMaxImageSize = 1024 * 1024 * 1024 * 10
		// Max meta size
		DefaultMaxMetaSize = 1024 * 1024 * 10
	)

	// parse content type and params according to RFC 1521
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		s.view.RenderError(w, err, http.StatusBadRequest)
		return
	}

	mr := multipart.NewReader(r.Body, params["boundary"])

	constructor, imagePart, err := s.handleMeta(mr, DefaultMaxMetaSize)
	if err != nil || imagePart == nil {
		s.view.RenderError(w, err, http.StatusBadRequest)
		return
	}

	imageFile, status, err := s.handleImage(imagePart, DefaultMaxImageSize)
	if err != nil {
		s.view.RenderError(w, err, status)
		return
	}
	defer os.Remove(imageFile.Name())
	defer imageFile.Close()

	imgId, err := s.model.CreateImage(imageFile, constructor)
	if err != nil {
		// TODO: check if this is bad request or internal error
		s.view.RenderError(w, err, http.StatusInternalServerError)
		return
	}

	s.view.RenderSuccessPost(w, r, imgId)
	return
}

// Meta part of multipart meta/image request handler.
// Parses meta body, returns image constructor, success code and nil on success.
func (s *SoftwareImagesController) handleMeta(mr *multipart.Reader, maxMetaSize int64) (*images.SoftwareImageConstructor, *multipart.Part, error) {
	constructor := &images.SoftwareImageConstructor{}
	for {
		p, err := mr.NextPart()
		if err != nil {
			return nil, nil, errors.Wrap(err, "Request does not contain firmware part")
		}
		switch p.FormName() {
		case "name":
			constructor.Name, err = s.getFormFieldValue(p, maxMetaSize)
			if err != nil {
				return nil, nil, err
			}
		case "yocto_id":
			constructor.YoctoId, err = s.getFormFieldValue(p, maxMetaSize)
			if err != nil {
				return nil, nil, err
			}
		case "device_type":
			constructor.DeviceType, err = s.getFormFieldValue(p, maxMetaSize)
			if err != nil {
				return nil, nil, err
			}
		case "checksum":
			constructor.Checksum, err = s.getFormFieldValue(p, maxMetaSize)
			if err != nil {
				return nil, nil, err
			}
		case "description":
			constructor.Description, err = s.getFormFieldValue(p, maxMetaSize)
			if err != nil {
				return nil, nil, err
			}
		case "firmware":
			if err := constructor.Validate(); err != nil {
				return nil, nil, errors.Wrap(err, "Validating metadata")
			}
			return constructor, p, nil
		}
	}
}

// Image part of multipart meta/image request handler.
// Saves uploaded image in temporary file.
// Returns temporary file name, success code and nil on success.
func (s *SoftwareImagesController) handleImage(p *multipart.Part, maxImageSize int64) (*os.File, int, error) {
	// HTML form can't set specific content-type, it's automatic, if not empty - it's a file
	if p.Header.Get("Content-Type") == "" {
		return nil, http.StatusBadRequest, errors.New("Last part should be an image")
	}
	tmpfile, err := ioutil.TempFile("", "firmware-")
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}

	n, err := io.CopyN(tmpfile, p, maxImageSize+1)
	if err != nil && err != io.EOF {
		return nil, http.StatusBadRequest, errors.Wrap(err, "Request body invalid")
	}
	if n == maxImageSize+1 {
		return nil, http.StatusBadRequest, errors.New("Image file too large")
	}

	return tmpfile, http.StatusOK, nil
}

func (s *SoftwareImagesController) getFormFieldValue(p *multipart.Part, maxMetaSize int64) (*string, error) {
	metaReader := io.LimitReader(p, maxMetaSize)
	bytes, err := ioutil.ReadAll(metaReader)
	if err != nil && err != io.EOF {
		return nil, errors.Wrap(err, "Failed to obtain value for " + p.FormName())
	}

	strValue := string(bytes)
	return &strValue, nil
}

func (s SoftwareImagesController) getSoftwareImageConstructorFromBody(r *rest.Request) (*images.SoftwareImageConstructor, error) {

	var constructor *images.SoftwareImageConstructor

	if err := r.DecodeJsonPayload(&constructor); err != nil {
		return nil, err
	}

	if err := constructor.Validate(); err != nil {
		return nil, err
	}

	return constructor, nil
}
