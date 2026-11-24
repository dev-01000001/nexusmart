export interface ShopSizeRow {
  size: "S" | "M" | "L" | "XL" | "2XL";
  heightMin: number;
  heightMax: number;
  weightMin: number;
  weightMax: number;
  chestOrWaistCm: string;
  lengthCm: string;
  shoulderCm?: string;
}

export interface ShopSizeCharts {
  top: ShopSizeRow[];
  bottom: ShopSizeRow[];
  fitBias: "slim" | "regular" | "relaxed";
  sizeNote: string;
}

export interface ShopFactoryProfile {
  factoryName: string;
  factoryAddress: string;
  fiberOrigin: string;
  qcProcess: string[];
  partnerSince: number;
  monthlyCapacity: string;
  certifications: string[];
}

export interface ShopCollectionSummary {
  slug: string;
  title: string;
  subtitle: string;
  productIds: string[];
}

export interface ShopInternalMenuItem {
  label: string;
  categorySlug?: string;
  collectionSlug?: string;
}

export interface Shop {
  id: string;
  slug: string;
  name: string;
  shortName: string;
  city: string;
  provinceCode: string;
  specialty: string;
  foundedYear: number;
  responseTime: string;
  fulfillmentRate: string;
  returnPolicy: string;
  warehouseAddress: string;
  story: string;
  accentDot: string;
  // Wave 2 extensions (optional for 100% backward compatibility)
  freeShipThreshold?: number; // integer VND
  fastDeliveryEnabled?: boolean;
  fastDeliveryFee?: number; // integer VND
  fastDeliveryNote?: string;
  returnDays?: number;
  twoWayExchange?: boolean;
  verifiedBadge?: string;
  factoryProfile?: ShopFactoryProfile;
  sizeCharts?: ShopSizeCharts;
  collections?: ShopCollectionSummary[];
  internalMenu?: ShopInternalMenuItem[];
}

export const SHOPS: Shop[] = [
  {
    id: "shop-bach-dang",
    slug: "xuong-det-bach-dang",
    name: "Xưởng Dệt Bạch Đằng",
    shortName: "Bạch Đằng",
    city: "Thành phố Hải Phòng",
    provinceCode: "HP",
    specialty: "Áo thun cotton chải kỹ 220gsm và đồ lót sợi modal",
    foundedYear: 2019,
    responseTime: "Trong 15 phút",
    fulfillmentRate: "98,6%",
    returnPolicy: "Đổi trả miễn phí 7 ngày, bưu tá đổi size 2 chiều tận nơi",
    warehouseAddress: "Số 42 đường Lạch Tray, Phường Ngô Quyền, Thành phố Hải Phòng",
    story:
      "Chủ động khâu dệt kim tròn và xử lý chống co rút trước khi cắt rập. Tập trung vào đồ mặc hằng ngày bền form sau nhiều lần giặt máy.",
    accentDot: "#0d7480",
    freeShipThreshold: 199000,
    fastDeliveryEnabled: false,
    fastDeliveryFee: 35000,
    fastDeliveryNote: "Giao tiêu chuẩn liên tỉnh 1-3 ngày từ kho Hải Phòng",
    returnDays: 7,
    twoWayExchange: true,
    verifiedBadge: "Xưởng chính hãng · Đổi trả 7 ngày",
    factoryProfile: {
      factoryName: "Nhà máy Dệt may Bạch Đằng số 2",
      factoryAddress: "Khu công nghiệp Tràng Duệ, Phường An Dương, Thành phố Hải Phòng",
      fiberOrigin: "Sợi bông chải kỹ Compact nhập khẩu và sợi gỗ sồi Modal kéo sợi tại Việt Nam",
      qcProcess: [
        "Kiểm định độ co rút sau 3 chu kỳ giặt thử trên từng cây vải mộc.",
        "Soi mật độ mũi kim và độ đều đường may vai, cổ bằng thước rập chuẩn.",
        "Kiểm tra độ bền màu ma sát khô/ướt trước khi gắn tem và đóng gói.",
      ],
      partnerSince: 2023,
      monthlyCapacity: "45.000 sản phẩm / tháng",
      certifications: [
        "Chuẩn an toàn dệt may không chất huỳnh quang",
        "Kiểm tra chất lượng 3 bước tại xưởng",
      ],
    },
    sizeCharts: {
      fitBias: "regular",
      sizeNote: "Form Regular gọn vai chuẩn Việt Nam; phân vân giữa 2 cỡ nên giữ cỡ đúng cân nặng.",
      top: [
        {
          size: "S",
          heightMin: 158,
          heightMax: 165,
          weightMin: 50,
          weightMax: 58,
          chestOrWaistCm: "94",
          lengthCm: "67",
          shoulderCm: "43",
        },
        {
          size: "M",
          heightMin: 165,
          heightMax: 171,
          weightMin: 59,
          weightMax: 66,
          chestOrWaistCm: "100",
          lengthCm: "69",
          shoulderCm: "45",
        },
        {
          size: "L",
          heightMin: 171,
          heightMax: 177,
          weightMin: 67,
          weightMax: 75,
          chestOrWaistCm: "106",
          lengthCm: "71",
          shoulderCm: "47",
        },
        {
          size: "XL",
          heightMin: 177,
          heightMax: 183,
          weightMin: 76,
          weightMax: 84,
          chestOrWaistCm: "112",
          lengthCm: "73",
          shoulderCm: "49",
        },
        {
          size: "2XL",
          heightMin: 183,
          heightMax: 190,
          weightMin: 85,
          weightMax: 94,
          chestOrWaistCm: "118",
          lengthCm: "75",
          shoulderCm: "51",
        },
      ],
      bottom: [
        {
          size: "S",
          heightMin: 158,
          heightMax: 166,
          weightMin: 50,
          weightMax: 58,
          chestOrWaistCm: "70-75",
          lengthCm: "24",
        },
        {
          size: "M",
          heightMin: 166,
          heightMax: 172,
          weightMin: 59,
          weightMax: 67,
          chestOrWaistCm: "76-82",
          lengthCm: "25",
        },
        {
          size: "L",
          heightMin: 172,
          heightMax: 178,
          weightMin: 68,
          weightMax: 76,
          chestOrWaistCm: "83-89",
          lengthCm: "26",
        },
        {
          size: "XL",
          heightMin: 178,
          heightMax: 184,
          weightMin: 77,
          weightMax: 85,
          chestOrWaistCm: "90-96",
          lengthCm: "27",
        },
        {
          size: "2XL",
          heightMin: 184,
          heightMax: 190,
          weightMin: 86,
          weightMax: 95,
          chestOrWaistCm: "97-104",
          lengthCm: "28",
        },
      ],
    },
    collections: [
      {
        slug: "cotton-220gsm-bach-dang",
        title: "Dòng Cotton Compact 220gsm",
        subtitle: "Ba màu chủ lực đứng dáng, ít xù lông cho tủ đồ hằng ngày.",
        productIds: ["prod-at-trang", "prod-at-xamda", "prod-at-xanhthan"],
      },
      {
        slug: "do-lot-modal-mem-mat",
        title: "Đồ lót sợi gỗ sồi Modal",
        subtitle: "May phẳng Flatlock êm da, hút ẩm cao.",
        productIds: ["prod-dolot-boxer"],
      },
    ],
    internalMenu: [
      { label: "Áo thun 220gsm", categorySlug: "ao-thun" },
      { label: "Đồ lót Modal", categorySlug: "do-lot" },
      { label: "BST Cotton Compact", collectionSlug: "co-ban-hang-ngay" },
    ],
  },
  {
    id: "shop-sai-gon",
    slug: "kho-basic-sai-gon",
    name: "Kho Basic Sài Gòn",
    shortName: "Basic Sài Gòn",
    city: "Thành phố Hồ Chí Minh",
    provinceCode: "HCM",
    specialty: "Đồ vận động dry-fit, quần short chạy bộ và áo khoác gió nhẹ",
    foundedYear: 2020,
    responseTime: "Trong 12 phút",
    fulfillmentRate: "99,1%",
    returnPolicy: "Đổi trả miễn phí 7 ngày, đổi size 2 chiều tận nhà",
    warehouseAddress: "Số 118 đường Nguyễn Văn Hưởng, Phường Thảo Điền, Thành phố Hồ Chí Minh",
    story:
      "Thiết kế cho khí hậu nóng ẩm phía Nam với sợi microfiber thoát mồ hôi nhanh, trọng lượng vải nhẹ và đường may phẳng giảm ma sát khi vận động.",
    accentDot: "#3e6b78",
    freeShipThreshold: 199000,
    fastDeliveryEnabled: true,
    fastDeliveryFee: 39000,
    fastDeliveryNote: "Giao nhanh nội thành TP.HCM trong 2-4 giờ (đặt trước 17:00)",
    returnDays: 7,
    twoWayExchange: true,
    verifiedBadge: "Xưởng chính hãng · Giao nhanh 2-4h",
    factoryProfile: {
      factoryName: "Xưởng May Vận động Basic Sài Gòn",
      factoryAddress: "Đường số 12, Khu chế xuất Tân Thuận, Thành phố Hồ Chí Minh",
      fiberOrigin: "Sợi Polyester tái chế Micro-mesh và Polyamide 4 chiều chuẩn thể thao",
      qcProcess: [
        "Đo thời gian thoát ẩm và trọng lượng vải theo từng lô nhập kho.",
        "Kiểm tra độ đàn hồi đường may chập phẳng 4 kim 6 chỉ ở vùng nách và đùi.",
        "Thử trượt nước nhẹ bề mặt ripstop trước khi đóng gói áo khoác gió.",
      ],
      partnerSince: 2023,
      monthlyCapacity: "52.000 sản phẩm / tháng",
      certifications: ["Sợi tái chế chuẩn truy xuất nguồn lô", "Đóng gói túi tự hủy sinh học"],
    },
    sizeCharts: {
      fitBias: "relaxed",
      sizeNote: "Form vận động và oversize hơi thoáng vai; thích mặc ôm gọn có thể lùi 1 cỡ.",
      top: [
        {
          size: "S",
          heightMin: 158,
          heightMax: 166,
          weightMin: 50,
          weightMax: 59,
          chestOrWaistCm: "96",
          lengthCm: "68",
          shoulderCm: "44",
        },
        {
          size: "M",
          heightMin: 166,
          heightMax: 173,
          weightMin: 60,
          weightMax: 68,
          chestOrWaistCm: "102",
          lengthCm: "70",
          shoulderCm: "46",
        },
        {
          size: "L",
          heightMin: 173,
          heightMax: 179,
          weightMin: 69,
          weightMax: 77,
          chestOrWaistCm: "108",
          lengthCm: "72",
          shoulderCm: "48",
        },
        {
          size: "XL",
          heightMin: 179,
          heightMax: 185,
          weightMin: 78,
          weightMax: 86,
          chestOrWaistCm: "114",
          lengthCm: "74",
          shoulderCm: "50",
        },
        {
          size: "2XL",
          heightMin: 185,
          heightMax: 192,
          weightMin: 87,
          weightMax: 96,
          chestOrWaistCm: "120",
          lengthCm: "76",
          shoulderCm: "52",
        },
      ],
      bottom: [
        {
          size: "S",
          heightMin: 158,
          heightMax: 166,
          weightMin: 50,
          weightMax: 59,
          chestOrWaistCm: "72-77",
          lengthCm: "43",
        },
        {
          size: "M",
          heightMin: 166,
          heightMax: 172,
          weightMin: 60,
          weightMax: 67,
          chestOrWaistCm: "78-82",
          lengthCm: "45",
        },
        {
          size: "L",
          heightMin: 172,
          heightMax: 178,
          weightMin: 68,
          weightMax: 76,
          chestOrWaistCm: "83-87",
          lengthCm: "47",
        },
        {
          size: "XL",
          heightMin: 178,
          heightMax: 184,
          weightMin: 77,
          weightMax: 85,
          chestOrWaistCm: "88-93",
          lengthCm: "49",
        },
        {
          size: "2XL",
          heightMin: 184,
          heightMax: 190,
          weightMin: 86,
          weightMax: 95,
          chestOrWaistCm: "94-99",
          lengthCm: "51",
        },
      ],
    },
    collections: [
      {
        slug: "chay-bo-sai-gon",
        title: "Bộ đồ chạy bộ siêu nhẹ",
        subtitle: "Dry-fit 145gsm và quần short 5 inch 118gsm khô nhanh.",
        productIds: ["prod-at-dryfit", "prod-short-run", "prod-aokhoac-gio"],
      },
      {
        slug: "street-basic-sai-gon",
        title: "Basic rộng thoáng hằng ngày",
        subtitle: "Áo 260gsm phối quần short kaki 7 inch.",
        productIds: ["prod-at-oversize", "prod-short-daily"],
      },
    ],
    internalMenu: [
      { label: "Đồ thể thao", collectionSlug: "chay-bo-tap-luyen" },
      { label: "Quần short", categorySlug: "quan-short" },
      { label: "Áo khoác gió", categorySlug: "tat-phu-kien" },
    ],
  },
  {
    id: "shop-da-nang",
    slug: "soi-tu-nhien-da-nang",
    name: "Sợi Tự Nhiên Đà Nẵng",
    shortName: "Sợi Tự Nhiên",
    city: "Thành phố Đà Nẵng",
    provinceCode: "DN",
    specialty: "Áo polo mắt chim pique, quần short linen và đồ lót sợi tre",
    foundedYear: 2021,
    responseTime: "Trong 18 phút",
    fulfillmentRate: "98,2%",
    returnPolicy: "Đổi trả miễn phí 7 ngày, đổi size 2 chiều tận nơi",
    warehouseAddress: "Số 76 đường Bạch Đằng, Phường Hải Châu, Thành phố Đà Nẵng",
    story:
      "Ưu tiên chất liệu sợi thực vật thoáng khí như lanh pha bông và sợi tre thiên nhiên, nhuộm tông trung tính dễ phối từ công sở đến cuối tuần.",
    accentDot: "#5a7668",
    freeShipThreshold: 229000,
    fastDeliveryEnabled: true,
    fastDeliveryFee: 35000,
    fastDeliveryNote: "Giao nhanh nội thành Đà Nẵng trong 3 giờ",
    returnDays: 7,
    twoWayExchange: true,
    verifiedBadge: "Xưởng chính hãng · Sợi thực vật",
    factoryProfile: {
      factoryName: "Xưởng Sợi Tự Nhiên Miền Trung",
      factoryAddress: "Khu công nghiệp Hòa Khánh, Phường Liên Chiểu, Thành phố Đà Nẵng",
      fiberOrigin: "Sợi tre Bamboo kháng mùi tự nhiên, lanh Linen pha bông và Cotton Pique chải kỹ",
      qcProcess: [
        "Giặt mềm enzyme sinh học để sợi lanh và sợi tre chạm dịu ngay lần mặc đầu.",
        "Kiểm tra độ thẳng của nẹp cổ polo và bo tay dệt rib 1:1 sau hấp hơi.",
        "Hút ẩm và kiểm định trọng lượng sợi trên từng lô trước khi xuất kho.",
      ],
      partnerSince: 2024,
      monthlyCapacity: "28.000 sản phẩm / tháng",
      certifications: ["Nhuộm hoạt tính tiết kiệm nước", "Thành viên chương trình Sợi Xanh Nexus"],
    },
    sizeCharts: {
      fitBias: "regular",
      sizeNote: "Form công sở gọn gàng, chiều dài thân áo vừa vặn khi sơ vin hoặc thả ngoài.",
      top: [
        {
          size: "S",
          heightMin: 159,
          heightMax: 165,
          weightMin: 51,
          weightMax: 58,
          chestOrWaistCm: "94",
          lengthCm: "67",
          shoulderCm: "43",
        },
        {
          size: "M",
          heightMin: 165,
          heightMax: 172,
          weightMin: 59,
          weightMax: 67,
          chestOrWaistCm: "100",
          lengthCm: "69",
          shoulderCm: "45",
        },
        {
          size: "L",
          heightMin: 172,
          heightMax: 178,
          weightMin: 68,
          weightMax: 75,
          chestOrWaistCm: "106",
          lengthCm: "71",
          shoulderCm: "47",
        },
        {
          size: "XL",
          heightMin: 178,
          heightMax: 183,
          weightMin: 76,
          weightMax: 83,
          chestOrWaistCm: "112",
          lengthCm: "73",
          shoulderCm: "49",
        },
        {
          size: "2XL",
          heightMin: 183,
          heightMax: 189,
          weightMin: 84,
          weightMax: 92,
          chestOrWaistCm: "118",
          lengthCm: "75",
          shoulderCm: "51",
        },
      ],
      bottom: [
        {
          size: "S",
          heightMin: 158,
          heightMax: 166,
          weightMin: 50,
          weightMax: 58,
          chestOrWaistCm: "72-76",
          lengthCm: "44",
        },
        {
          size: "M",
          heightMin: 166,
          heightMax: 172,
          weightMin: 59,
          weightMax: 67,
          chestOrWaistCm: "77-81",
          lengthCm: "46",
        },
        {
          size: "L",
          heightMin: 172,
          heightMax: 178,
          weightMin: 68,
          weightMax: 76,
          chestOrWaistCm: "82-86",
          lengthCm: "48",
        },
        {
          size: "XL",
          heightMin: 178,
          heightMax: 184,
          weightMin: 77,
          weightMax: 85,
          chestOrWaistCm: "87-92",
          lengthCm: "50",
        },
        {
          size: "2XL",
          heightMin: 184,
          heightMax: 190,
          weightMin: 86,
          weightMax: 94,
          chestOrWaistCm: "93-98",
          lengthCm: "51",
        },
      ],
    },
    collections: [
      {
        slug: "cong-so-soi-tu-nhien",
        title: "Polo Pique & Short Linen",
        subtitle: "Thoáng nhẹ cho công sở mùa nóng và du lịch cuối tuần.",
        productIds: ["prod-polo-kem", "prod-polo-xanhda", "prod-short-linen"],
      },
      {
        slug: "soi-tre-khang-mui",
        title: "Sợi tre Bamboo thiên nhiên",
        subtitle: "Đồ lót sợi tre mát nhẹ, tem ép nhiệt không gây ngứa.",
        productIds: ["prod-dolot-brief"],
      },
    ],
    internalMenu: [
      { label: "Áo Polo Pique", categorySlug: "ao-polo" },
      { label: "Short Linen", categorySlug: "quan-short" },
      { label: "Đồ lót sợi tre", categorySlug: "do-lot" },
    ],
  },
  {
    id: "shop-ha-noi",
    slug: "det-kim-ha-noi",
    name: "Dệt Kim Hà Nội",
    shortName: "Dệt Kim Hà Nội",
    city: "Thành phố Hà Nội",
    provinceCode: "HN",
    specialty: "Áo polo dệt kim, quần jogger nỉ chân cua và tất cotton gân",
    foundedYear: 2018,
    responseTime: "Trong 20 phút",
    fulfillmentRate: "97,9%",
    returnPolicy: "Đổi trả miễn phí 7 ngày, bù lẻ chiếc tất thất lạc",
    warehouseAddress: "Số 19 phố Minh Khai, Phường Hai Bà Trưng, Thành phố Hà Nội",
    story:
      "Kế thừa tay nghề dệt kim truyền thống miền Bắc, chú trọng độ đứng dáng của cổ áo polo, bo gấu jogger dày dặn và dòng tất cotton chải kỹ.",
    accentDot: "#4a5d68",
    freeShipThreshold: 199000,
    fastDeliveryEnabled: true,
    fastDeliveryFee: 39000,
    fastDeliveryNote: "Giao nhanh nội thành Hà Nội trong 2-4 giờ (đặt trước 17:00)",
    returnDays: 7,
    twoWayExchange: true,
    verifiedBadge: "Xưởng chính hãng · Bù lẻ tất",
    factoryProfile: {
      factoryName: "Xưởng Dệt kim truyền thống Minh Khai",
      factoryAddress: "Cụm công nghiệp Ninh Hiệp, Xã Gia Lâm, Thành phố Hà Nội",
      fiberOrigin:
        "Sợi Viscose mảnh dệt kim máy phẳng, Cotton French Terry 320gsm và Cotton chải kỹ",
      qcProcess: [
        "Kiểm tra độ đều mắt dệt trên bàn soi sáng đối với áo polo dệt kim.",
        "Thử giãn bo gấu quần jogger và cổ tất qua 500 nhịp kéo cơ học.",
        "Rà kim loại và nối mũi tất liền mạch không cộm trước khi đóng hộp.",
      ],
      partnerSince: 2023,
      monthlyCapacity: "38.000 sản phẩm / tháng",
      certifications: ["Xưởng dệt kim đạt chuẩn nội địa", "Cam kết bù lẻ 1 chiếc tất thất lạc"],
    },
    sizeCharts: {
      fitBias: "slim",
      sizeNote: "Form dệt kim và tapered jogger ôm gọn vai/bắp chân; thích rộng rãi nên tăng 1 cỡ.",
      top: [
        {
          size: "S",
          heightMin: 156,
          heightMax: 163,
          weightMin: 48,
          weightMax: 56,
          chestOrWaistCm: "92",
          lengthCm: "66",
          shoulderCm: "42",
        },
        {
          size: "M",
          heightMin: 163,
          heightMax: 170,
          weightMin: 57,
          weightMax: 64,
          chestOrWaistCm: "98",
          lengthCm: "68",
          shoulderCm: "44",
        },
        {
          size: "L",
          heightMin: 170,
          heightMax: 176,
          weightMin: 65,
          weightMax: 73,
          chestOrWaistCm: "104",
          lengthCm: "70",
          shoulderCm: "46",
        },
        {
          size: "XL",
          heightMin: 176,
          heightMax: 182,
          weightMin: 74,
          weightMax: 82,
          chestOrWaistCm: "110",
          lengthCm: "72",
          shoulderCm: "48",
        },
        {
          size: "2XL",
          heightMin: 182,
          heightMax: 188,
          weightMin: 83,
          weightMax: 90,
          chestOrWaistCm: "116",
          lengthCm: "74",
          shoulderCm: "50",
        },
      ],
      bottom: [
        {
          size: "S",
          heightMin: 158,
          heightMax: 165,
          weightMin: 50,
          weightMax: 57,
          chestOrWaistCm: "71-75",
          lengthCm: "94",
        },
        {
          size: "M",
          heightMin: 165,
          heightMax: 171,
          weightMin: 58,
          weightMax: 65,
          chestOrWaistCm: "76-80",
          lengthCm: "96",
        },
        {
          size: "L",
          heightMin: 171,
          heightMax: 177,
          weightMin: 66,
          weightMax: 74,
          chestOrWaistCm: "81-85",
          lengthCm: "98",
        },
        {
          size: "XL",
          heightMin: 177,
          heightMax: 183,
          weightMin: 75,
          weightMax: 83,
          chestOrWaistCm: "86-91",
          lengthCm: "100",
        },
        {
          size: "2XL",
          heightMin: 183,
          heightMax: 189,
          weightMin: 84,
          weightMax: 92,
          chestOrWaistCm: "92-97",
          lengthCm: "102",
        },
      ],
    },
    collections: [
      {
        slug: "thu-dong-det-kim",
        title: "Dệt kim & Nỉ chân cua 320gsm",
        subtitle: "Đứng phom cho tiết trời mát và môi trường điều hòa.",
        productIds: ["prod-polo-detkim", "prod-jogger-terry", "prod-jogger-tech"],
      },
      {
        slug: "combo-tat-cotton-ha-noi",
        title: "Tất cotton gân & thể thao",
        subtitle: "Nối mũi liền mạch, áp dụng chính sách bù lẻ chiếc thất lạc.",
        productIds: ["prod-tat-cotrung", "prod-tat-cothap"],
      },
    ],
    internalMenu: [
      { label: "Polo dệt kim", categorySlug: "ao-polo" },
      { label: "Quần Jogger", categorySlug: "quan-jogger" },
      { label: "Combo Tất", categorySlug: "tat-phu-kien" },
    ],
  },
];

export function getShopById(id: string): Shop | undefined {
  return SHOPS.find((s) => s.id === id);
}

export function getShopBySlug(slug: string): Shop | undefined {
  return SHOPS.find((s) => s.slug === slug);
}
