# Image `web`: keempat situs statis loong.lat dalam satu nginx. Konteks build disiapkan oleh build.sh (folder sites/).
FROM nginx:1.29-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY sites/ /srv/
HEALTHCHECK --interval=10s --timeout=3s --retries=5 CMD wget -q -O /dev/null http://127.0.0.1/healthz || exit 1
