import { DocsPageView, docsMetadata, docsStaticParams } from "@/routes/docs-page";

type Props = { params: Promise<{ slug?: string[] }> };

// Only the generated slugs exist; anything else is the global 404 document.
export const dynamicParams = false;
export const generateStaticParams = () => docsStaticParams("en");
export const generateMetadata = (props: Props) => docsMetadata("en", props);

export default function Page(props: Props) {
  return <DocsPageView lang="en" {...props} />;
}
