import { docs } from "@/.source/server";
import { loader } from "fumadocs-core/source";
import { i18n } from "@/lib/i18n";

export const source = loader({
  baseUrl: "/docs", // locale prefix is added by the loader (hideLocale: never)
  source: docs.toFumadocsSource(),
  i18n,
});
