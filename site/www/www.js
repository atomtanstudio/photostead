// www.photostead.com: every request goes to the same path on photostead.com, for good (301).
export default {
  fetch(request) {
    const url = new URL(request.url);
    url.protocol = 'https:';
    url.hostname = 'photostead.com';
    url.port = '';
    return Response.redirect(url.toString(), 301);
  },
};
