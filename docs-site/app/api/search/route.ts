import { source } from "@/lib/source";
import { createFromSource } from "fumadocs-core/search/server";

const search = createFromSource(source);

/** Search results are an API, not a page: keep them out of search indexes. */
export async function GET(request: Request) {
  const response = await search.GET(request);
  response.headers.set("X-Robots-Tag", "noindex");
  return response;
}
