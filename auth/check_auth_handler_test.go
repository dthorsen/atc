package auth_test

import (
	"bytes"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"

	"code.cloudfoundry.org/lager/lagertest"
	"github.com/concourse/atc/auth"
	"github.com/concourse/atc/auth/authfakes"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("CheckAuthenticationHandler", func() {
	var (
		fakeValidator         *authfakes.FakeValidator
		fakeUserContextReader *authfakes.FakeUserContextReader
		fakeRejector          *authfakes.FakeRejector

		server *httptest.Server
		client *http.Client
		logger *lagertest.TestLogger
	)

	simpleHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buffer := bytes.NewBufferString("simple ")

		io.Copy(w, buffer)
		io.Copy(w, r.Body)
	})

	BeforeEach(func() {
		fakeValidator = new(authfakes.FakeValidator)
		fakeUserContextReader = new(authfakes.FakeUserContextReader)
		fakeRejector = new(authfakes.FakeRejector)

		fakeRejector.UnauthorizedStub = func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusUnauthorized)
		}

		server = httptest.NewServer(auth.WrapHandler(
			logger,
			auth.CheckAuthenticationHandler(
				simpleHandler,
				fakeRejector,
			),
			fakeValidator,
			fakeUserContextReader,
		))

		client = &http.Client{
			Transport: &http.Transport{},
		}
	})

	Context("when a request is made", func() {
		var request *http.Request
		var response *http.Response

		BeforeEach(func() {
			var err error

			request, err = http.NewRequest("GET", server.URL, bytes.NewBufferString("hello"))
			Expect(err).NotTo(HaveOccurred())
		})

		JustBeforeEach(func() {
			var err error

			response, err = client.Do(request)
			Expect(err).NotTo(HaveOccurred())
		})

		Context("when the validator returns true", func() {
			BeforeEach(func() {
				fakeValidator.IsAuthenticatedReturns(true)
			})

			It("returns 200", func() {
				Expect(response.StatusCode).To(Equal(http.StatusOK))
			})

			It("proxies to the handler", func() {
				responseBody, err := ioutil.ReadAll(response.Body)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(responseBody)).To(Equal("simple hello"))
			})
		})

		Context("when the validator returns false", func() {
			BeforeEach(func() {
				fakeValidator.IsAuthenticatedReturns(false)
			})

			It("rejects the request", func() {
				Expect(response.StatusCode).To(Equal(http.StatusUnauthorized))
				responseBody, err := ioutil.ReadAll(response.Body)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(responseBody)).To(Equal("nope\n"))
			})
		})
	})
})
