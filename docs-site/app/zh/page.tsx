import { HomePage, homeMetadata } from "@/routes/home-page";

export const metadata = homeMetadata("zh");

export default function Page() {
  return <HomePage lang="zh" />;
}
